package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

const defaultBase = "https://api.aquavoice.com/v1"

// WAV header is ~78 bytes; anything this small is a take with no audio.
const emptyTake = 200

var errEmpty = errors.New("empty take, nothing sent")

var recorders = [][]string{
	{"parecord", "{out}"},
	{"ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "pulse", "-i", "default", "-y", "{out}"},
}

func main() {
	raw := flag.Bool("raw", false, "raw transcription via /audio/transcriptions (needs an Avalon key)")
	lang := flag.String("language", "auto", "language code passed to the API")
	model := flag.String("model", "avalon-v1.5", "Avalon model (transcription mode)")
	noClip := flag.Bool("no-clipboard", false, "print to stdout only")
	verbose := flag.Bool("verbose", false, "report take stats and show a spinner while waiting")
	flag.Parse()

	if err := run(!*raw, *lang, *model, *noClip, *verbose); err != nil {
		if !errors.Is(err, errEmpty) {
			fmt.Fprintln(os.Stderr, "aqua:", err)
			os.Exit(1)
		}
	}
}

func run(dictate bool, lang, model string, noClip, verbose bool) error {
	key, err := apiKey(dictate)
	if err != nil {
		return err
	}
	audio, dur, err := record()
	if err != nil {
		return err
	}
	if verbose {
		fmt.Fprintf(os.Stderr, "Recording done: %.1fs, %s WAV\n", dur.Seconds(), humanSize(len(audio)))
	}
	base := os.Getenv("AQUA_BASE_URL")
	if base == "" {
		base = defaultBase
	}
	stopSpin := func() {}
	if verbose {
		stopSpin = startSpinner()
	}
	res, err := transcribe(&http.Client{Timeout: 220 * time.Second}, base, key, audio, dictate, model, lang)
	stopSpin()
	if err != nil {
		return err
	}
	fmt.Println(res.Text)
	if verbose && res.SessionID != "" {
		fmt.Fprintln(os.Stderr, "session:", res.SessionID)
	}
	if !noClip {
		if err := copyToClipboard(res.Text); err != nil {
			fmt.Fprintln(os.Stderr, "aqua: clipboard:", err)
		}
	}
	return nil
}

func apiKey(dictate bool) (string, error) {
	if !dictate {
		if k := os.Getenv("AQUAVOICE_AVALON_KEY"); k != "" {
			return k, nil
		}
		if k := os.Getenv("AQUAVOICE_API_KEY"); k != "" {
			return k, nil
		}
		return "", errors.New("AQUAVOICE_AVALON_KEY is not set (--raw needs an Avalon key with transcription scope; AQUAVOICE_API_KEY works as fallback)")
	}
	if k := os.Getenv("AQUAVOICE_API_KEY"); k != "" {
		return k, nil
	}
	return "", errors.New("AQUAVOICE_API_KEY is not set (dictate mode needs an Aqua data key with write scope)")
}

func record() ([]byte, time.Duration, error) {
	var argv []string
	for _, c := range recorders {
		if path, err := exec.LookPath(c[0]); err == nil {
			argv = append([]string{path}, c[1:]...)
			break
		}
	}
	if argv == nil {
		return nil, 0, errors.New("no recorder found: install pulseaudio-utils (parecord) or ffmpeg")
	}
	f, err := os.CreateTemp("", "aqua-*.wav")
	if err != nil {
		return nil, 0, err
	}
	tmp := f.Name()
	f.Close()
	defer os.Remove(tmp)
	for i, a := range argv {
		if a == "{out}" {
			argv[i] = tmp
		}
	}

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, 0, err
	}
	start := time.Now()
	fmt.Fprintln(os.Stderr, "Recording… Enter/Ctrl-C to stop")

	enter := make(chan struct{})
	go func() {
		bufio.NewReader(os.Stdin).ReadBytes('\n')
		close(enter)
	}()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)

	select {
	case <-enter:
	case <-sig:
	}
	dur := time.Since(start)
	signal.Stop(sig)
	cmd.Process.Signal(os.Interrupt) // let the recorder finalize the WAV header
	cmd.Wait()                       // exit status is irrelevant; the file tells the truth
	audio, err := os.ReadFile(tmp)
	if err != nil {
		return nil, 0, err
	}
	if len(audio) < emptyTake {
		return nil, 0, errEmpty
	}
	return audio, dur, nil
}

type result struct {
	Text      string
	SessionID string // dictate mode only
}

func transcribe(client *http.Client, base, key string, audio []byte, dictate bool, model, lang string) (result, error) {
	field, url := "file", base+"/audio/transcriptions"
	if dictate {
		field, url = "audio", base+"/dictations"
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile(field, "take.wav")
	if err != nil {
		return result{}, err
	}
	fw.Write(audio)
	if dictate {
		w.WriteField("operation", "dictate")
	} else {
		w.WriteField("model", model)
	}
	w.WriteField("language", lang)
	w.Close()

	newReq := func() *http.Request {
		req, _ := http.NewRequest("POST", url, bytes.NewReader(buf.Bytes()))
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Content-Type", w.FormDataContentType())
		return req
	}
	if dictate {
		// lets a retry replay the result without a second charge
		idem := make([]byte, 16)
		rand.Read(idem)
		baseReq := newReq
		newReq = func() *http.Request {
			r := baseReq()
			r.Header.Set("Idempotency-Key", hex.EncodeToString(idem))
			return r
		}
	}

	resp, err := client.Do(newReq())
	if err != nil && dictate {
		resp, err = client.Do(newReq()) // closed connection mid-dictation: replay with same key
	}
	if err != nil {
		return result{}, err
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode == http.StatusGatewayTimeout && !dictate {
		var e struct {
			Error struct {
				JobID string `json:"job_id"`
			} `json:"error"`
		}
		if json.Unmarshal(body, &e) == nil && e.Error.JobID != "" {
			return pollJob(client, base, key, e.Error.JobID)
		}
	}
	if resp.StatusCode == http.StatusGatewayTimeout && dictate {
		return transcribeResp(client.Do(newReq()))
	}
	if resp.StatusCode/100 != 2 {
		return result{}, fmt.Errorf("API %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return parseResult(body)
}

func transcribeResp(resp *http.Response, err error) (result, error) {
	if err != nil {
		return result{}, err
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return result{}, fmt.Errorf("API %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return parseResult(body)
}

func pollJob(client *http.Client, base, key, jobID string) (result, error) {
	for i := 0; i < 120; i++ {
		fmt.Fprintln(os.Stderr, "Still processing…")
		time.Sleep(time.Second)
		req, _ := http.NewRequest("GET", base+"/audio/transcription-jobs/"+jobID+"/result", nil)
		req.Header.Set("Authorization", "Bearer "+key)
		resp, err := client.Do(req)
		if err != nil {
			return result{}, err
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusConflict {
			continue
		}
		if resp.StatusCode/100 != 2 {
			return result{}, fmt.Errorf("API %s: %s", resp.Status, strings.TrimSpace(string(body)))
		}
		return parseResult(body)
	}
	return result{}, errors.New("job still not complete after 2 minutes")
}

func parseResult(body []byte) (result, error) {
	var out struct {
		Text      string `json:"text"`
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return result{}, fmt.Errorf("bad response: %w", err)
	}
	return result{Text: out.Text, SessionID: out.SessionID}, nil
}

// startSpinner animates a waiting indicator on stderr; the returned func
// stops it and clears the line.
func startSpinner() func() {
	done, finished := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		frames := `-\|/`
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		start := time.Now()
		for i := 0; ; i++ {
			select {
			case <-done:
				fmt.Fprint(os.Stderr, "\r\033[K")
				return
			case <-t.C:
				fmt.Fprintf(os.Stderr, "\r%c Waiting for API… %.0fs", frames[i%len(frames)], time.Since(start).Seconds())
			}
		}
	}()
	return func() { close(done); <-finished }
}

func humanSize(n int) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.0f KB", float64(n)/1024)
}

func copyToClipboard(text string) error {
	path, err := exec.LookPath("wl-copy")
	if err != nil {
		return errors.New("wl-copy not found")
	}
	c := exec.Command(path)
	c.Stdin = strings.NewReader(text)
	return c.Run()
}
