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
	dictate := flag.Bool("dictate", false, "formatted dictation via /dictations (needs a write-scope key)")
	lang := flag.String("language", "auto", "language code passed to the API")
	model := flag.String("model", "avalon-v1.5", "Avalon model (transcription mode)")
	noClip := flag.Bool("no-clipboard", false, "print to stdout only")
	flag.Parse()

	if err := run(*dictate, *lang, *model, *noClip); err != nil {
		if !errors.Is(err, errEmpty) {
			fmt.Fprintln(os.Stderr, "aqua:", err)
			os.Exit(1)
		}
	}
}

func run(dictate bool, lang, model string, noClip bool) error {
	key, err := apiKey(dictate)
	if err != nil {
		return err
	}
	audio, err := record()
	if err != nil {
		return err
	}
	base := os.Getenv("AQUA_BASE_URL")
	if base == "" {
		base = defaultBase
	}
	text, err := transcribe(&http.Client{Timeout: 220 * time.Second}, base, key, audio, dictate, model, lang)
	if err != nil {
		return err
	}
	fmt.Println(text)
	if !noClip {
		if err := copyToClipboard(text); err != nil {
			fmt.Fprintln(os.Stderr, "aqua: clipboard:", err)
		}
	}
	return nil
}

func apiKey(dictate bool) (string, error) {
	if dictate {
		if k := os.Getenv("AQUAVOICE_API_KEY"); k != "" {
			return k, nil
		}
		return "", errors.New("AQUAVOICE_API_KEY is not set (--dictate needs an Aqua data key with write scope)")
	}
	if k := os.Getenv("AQUAVOICE_AVALON_KEY"); k != "" {
		return k, nil
	}
	if k := os.Getenv("AQUAVOICE_API_KEY"); k != "" {
		return k, nil
	}
	return "", errors.New("AQUAVOICE_AVALON_KEY is not set (need an Avalon key with transcription scope; AQUAVOICE_API_KEY works as fallback)")
}

func record() ([]byte, error) {
	var argv []string
	for _, c := range recorders {
		if path, err := exec.LookPath(c[0]); err == nil {
			argv = append([]string{path}, c[1:]...)
			break
		}
	}
	if argv == nil {
		return nil, errors.New("no recorder found: install pulseaudio-utils (parecord) or ffmpeg")
	}
	f, err := os.CreateTemp("", "aqua-*.wav")
	if err != nil {
		return nil, err
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
		return nil, err
	}
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
	signal.Stop(sig)
	cmd.Process.Signal(os.Interrupt) // let the recorder finalize the WAV header
	cmd.Wait()                       // exit status is irrelevant; the file tells the truth
	audio, err := os.ReadFile(tmp)
	if err != nil {
		return nil, err
	}
	if len(audio) < emptyTake {
		return nil, errEmpty
	}
	return audio, nil
}

func transcribe(client *http.Client, base, key string, audio []byte, dictate bool, model, lang string) (string, error) {
	field, url := "file", base+"/audio/transcriptions"
	if dictate {
		field, url = "audio", base+"/dictations"
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile(field, "take.wav")
	if err != nil {
		return "", err
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
		return "", err
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
		return "", fmt.Errorf("API %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return parseText(body)
}

func transcribeResp(resp *http.Response, err error) (string, error) {
	if err != nil {
		return "", err
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("API %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return parseText(body)
}

func pollJob(client *http.Client, base, key, jobID string) (string, error) {
	for i := 0; i < 120; i++ {
		fmt.Fprintln(os.Stderr, "Still processing…")
		time.Sleep(time.Second)
		req, _ := http.NewRequest("GET", base+"/audio/transcription-jobs/"+jobID+"/result", nil)
		req.Header.Set("Authorization", "Bearer "+key)
		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusConflict {
			continue
		}
		if resp.StatusCode/100 != 2 {
			return "", fmt.Errorf("API %s: %s", resp.Status, strings.TrimSpace(string(body)))
		}
		return parseText(body)
	}
	return "", errors.New("job still not complete after 2 minutes")
}

func parseText(body []byte) (string, error) {
	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("bad response: %w", err)
	}
	return out.Text, nil
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
