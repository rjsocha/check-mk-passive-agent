package main

import (
	"bytes"
	"compress/gzip"
	"crypto/md5"
	"encoding/hex"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	log.SetOutput(io.Discard)
	os.Exit(m.Run())
}

const agentData = "<<<check_mk>>>\nVersion: test\n"

type request struct {
	path    string
	method  string
	fields  map[string]string
	payload []byte
	noFile  bool
}

func gz(t *testing.T, data string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	w.Write([]byte(data))
	w.Close()
	return b.Bytes()
}

func sum(data string) string {
	d := md5.Sum([]byte(data))
	return hex.EncodeToString(d[:])
}

func valid(t *testing.T) request {
	return request{
		path:   "/push-agent/passive/web1.example.com",
		method: http.MethodPost,
		fields: map[string]string{
			"token":    "tok-1",
			"hostname": "web1.example.com",
			"md5":      sum(agentData),
		},
		payload: gz(t, agentData),
	}
}

func receiver(t *testing.T) *Receiver {
	t.Helper()
	return &Receiver{
		Config:     Config{Tokens: []string{"tok-0", "tok-1"}},
		Storage:    t.TempDir(),
		MaxBody:    1 << 20,
		MaxPayload: 1 << 20,
	}
}

func send(t *testing.T, rc *Receiver, rq request) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, v := range rq.fields {
		mw.WriteField(k, v)
	}
	if !rq.noFile {
		fw, _ := mw.CreateFormFile("payload", "payload.gz")
		fw.Write(rq.payload)
	}
	mw.Close()
	r := httptest.NewRequest(rq.method, rq.path, &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	rc.ServeHTTP(w, r)
	return w
}

func TestStore(t *testing.T) {
	rc := receiver(t)
	for _, path := range []string{"/push-agent/passive", "/push-agent/passive/web1.example.com"} {
		rq := valid(t)
		rq.path = path
		if w := send(t, rc, rq); w.Code != http.StatusOK {
			t.Fatalf("%s: code %d, body %q", path, w.Code, w.Body.String())
		}
	}
	file := filepath.Join(rc.Storage, "web1.example.com")
	got, err := os.ReadFile(file)
	if err != nil || string(got) != agentData {
		t.Fatalf("stored %q, err %v", got, err)
	}
	st, _ := os.Stat(file)
	if st.Mode().Perm() != 0o640 {
		t.Fatalf("mode %v", st.Mode().Perm())
	}
	entries, _ := os.ReadDir(rc.Storage)
	if len(entries) != 1 {
		t.Fatalf("leftover files: %d entries", len(entries))
	}
}

func TestReject(t *testing.T) {
	cases := []struct {
		name   string
		change func(*request)
		code   int
		body   string
	}{
		{"wrong path", func(r *request) { r.path = "/push-agent/other" }, 404, ""},
		{"prefix without slash", func(r *request) { r.path = "/push-agent/passivex" }, 404, ""},
		{"method", func(r *request) { r.method = http.MethodGet }, 405, ""},
		{"no token", func(r *request) { delete(r.fields, "token") }, 406, ""},
		{"bad token", func(r *request) { r.fields["token"] = "tok-x" }, 404, ""},
		{"no hostname", func(r *request) { delete(r.fields, "hostname") }, 405, ""},
		{"no md5", func(r *request) { delete(r.fields, "md5") }, 405, ""},
		{"no payload", func(r *request) { r.noFile = true }, 400, ""},
		{"hostname slash", func(r *request) { r.fields["hostname"] = "a/b" }, 403, "FAIL:HOSTNAME"},
		{"hostname dotdot", func(r *request) { r.fields["hostname"] = ".." }, 403, "FAIL:HOSTNAME"},
		{"hostname hyphen", func(r *request) { r.fields["hostname"] = "-web" }, 403, "FAIL:HOSTNAME"},
		{"md5 mismatch", func(r *request) { r.fields["md5"] = sum("other") }, 403, "FAIL:WRITE"},
		{"not gzip", func(r *request) { r.payload = []byte(agentData) }, 403, "FAIL:WRITE"},
		{"payload too large", func(r *request) {
			big := strings.Repeat("x", 2<<20)
			r.payload = gz(t, big)
			r.fields["md5"] = sum(big)
		}, 403, "FAIL:WRITE"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rc := receiver(t)
			rq := valid(t)
			c.change(&rq)
			w := send(t, rc, rq)
			if w.Code != c.code || w.Body.String() != c.body {
				t.Fatalf("got %d %q, want %d %q", w.Code, w.Body.String(), c.code, c.body)
			}
			entries, _ := os.ReadDir(rc.Storage)
			if len(entries) != 0 {
				t.Fatalf("storage not empty: %d entries", len(entries))
			}
		})
	}
}

func TestBodyTooLarge(t *testing.T) {
	rc := receiver(t)
	rc.MaxBody = 1024
	rq := valid(t)
	rq.payload = bytes.Repeat([]byte{1}, 4096)
	if w := send(t, rc, rq); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("code %d", w.Code)
	}
}

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	write := func(s string) string {
		p := filepath.Join(dir, "config.json")
		os.WriteFile(p, []byte(s), 0o600)
		return p
	}
	if _, err := loadConfig(write(`{"tokens":["t"]}`)); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		`{"tokens":[]}`,
		`{}`,
		`{"secrets":[""]}`,
		`{"tokens":[""]}`,
		`{broken`,
	} {
		if _, err := loadConfig(write(bad)); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}

func TestBasicAuthHeaderIgnored(t *testing.T) {
	rc := receiver(t)
	rq := valid(t)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, v := range rq.fields {
		mw.WriteField(k, v)
	}
	fw, _ := mw.CreateFormFile("payload", "payload.gz")
	fw.Write(rq.payload)
	mw.Close()
	r := httptest.NewRequest(http.MethodPost, rq.path, &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.SetBasicAuth("passive", "anything")
	w := httptest.NewRecorder()
	rc.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("code %d", w.Code)
	}
}

func TestPerHostToken(t *testing.T) {
	rc := receiver(t)
	rc.Config = Config{Secrets: []string{"s3cr3t"}}

	rq := valid(t)
	rq.fields["token"] = hostToken("s3cr3t", "web1.example.com")
	if w := send(t, rc, rq); w.Code != http.StatusOK {
		t.Fatalf("own token: code %d", w.Code)
	}

	rq = valid(t)
	rq.fields["token"] = hostToken("s3cr3t", "other.example.com")
	if w := send(t, rc, rq); w.Code != http.StatusNotFound {
		t.Fatalf("token of another host: code %d", w.Code)
	}

	rq = valid(t)
	rq.fields["token"] = "s3cr3t"
	if w := send(t, rc, rq); w.Code != http.StatusNotFound {
		t.Fatalf("secret used as token: code %d", w.Code)
	}
}

func TestBothModes(t *testing.T) {
	rc := receiver(t)
	rc.Config = Config{Tokens: []string{"shared"}, Secrets: []string{"s3cr3t"}}
	for _, token := range []string{"shared", hostToken("s3cr3t", "web1.example.com")} {
		rq := valid(t)
		rq.fields["token"] = token
		if w := send(t, rc, rq); w.Code != http.StatusOK {
			t.Fatalf("token %q: code %d", token, w.Code)
		}
	}
}

func TestHostTokenValue(t *testing.T) {
	const known = "9b5c6ffb3221672dad920f4302ab9c1a1d9be939c11f58471d859dfb2e21faef"
	if got := hostToken("s3cr3t", "web1.example.com"); got != known {
		t.Fatalf("hostToken = %s, want %s", got, known)
	}
	if hostToken("a", "b") == hostToken("a", "c") {
		t.Fatal("token does not depend on hostname")
	}
	if hostToken("a", "b") == hostToken("x", "b") {
		t.Fatal("token does not depend on secret")
	}
}
