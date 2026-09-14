package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func wav() []byte { return append([]byte("RIFF"), make([]byte, 300)...) }

func TestRaw(t *testing.T) {
	var gotAuth, gotModel, gotLang, gotFile string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		r.ParseMultipartForm(1 << 20)
		gotModel = r.FormValue("model")
		gotLang = r.FormValue("language")
		f, _, err := r.FormFile("file")
		if err == nil {
			b := make([]byte, 4)
			f.Read(b)
			gotFile = string(b)
		}
		json.NewEncoder(w).Encode(map[string]string{"text": "hello aqua"})
	}))
	defer srv.Close()

	res, err := transcribe(srv.Client(), srv.URL, "k", wav(), false, "avalon-v1.5", "auto")
	if err != nil || res.Text != "hello aqua" || res.SessionID != "" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if gotAuth != "Bearer k" || gotModel != "avalon-v1.5" || gotLang != "auto" || gotFile != "RIFF" {
		t.Fatalf("auth=%q model=%q lang=%q file=%q", gotAuth, gotModel, gotLang, gotFile)
	}
}

func TestDictate(t *testing.T) {
	var gotOp, gotIdem string
	var sawAudio bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotIdem = r.Header.Get("Idempotency-Key")
		r.ParseMultipartForm(1 << 20)
		gotOp = r.FormValue("operation")
		_, _, err := r.FormFile("audio")
		sawAudio = err == nil
		json.NewEncoder(w).Encode(map[string]string{"text": "Hello, Aqua!", "session_id": "ses_1"})
	}))
	defer srv.Close()

	res, err := transcribe(srv.Client(), srv.URL, "k", wav(), true, "", "ru")
	if err != nil || res.Text != "Hello, Aqua!" || res.SessionID != "ses_1" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if gotOp != "dictate" || !sawAudio || gotIdem == "" {
		t.Fatalf("op=%q audio=%v idem=%q", gotOp, sawAudio, gotIdem)
	}
}

func Test504PollsJob(t *testing.T) {
	polled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			polled = true
			json.NewEncoder(w).Encode(map[string]string{"text": "late text"})
			return
		}
		w.WriteHeader(http.StatusGatewayTimeout)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{"job_id": "req_1"},
		})
	}))
	defer srv.Close()

	res, err := transcribe(srv.Client(), srv.URL, "k", wav(), false, "m", "auto")
	if err != nil || res.Text != "late text" || !polled {
		t.Fatalf("res=%+v polled=%v err=%v", res, polled, err)
	}
}
