package main

import (
	"compress/gzip"
	"crypto/md5"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const endpoint = "/push-agent/passive"

var label = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

type Receiver struct {
	Config     Config
	Storage    string
	MaxBody    int64
	MaxPayload int64
}

func validHostname(name string) bool {
	if name == "" || len(name) > 253 {
		return false
	}
	for _, l := range strings.Split(name, ".") {
		if !label.MatchString(l) {
			return false
		}
	}
	return true
}

func equal(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func hostToken(secret, hostname string) string {
	digest := sha256.Sum256([]byte(secret + ":" + hostname))
	return hex.EncodeToString(digest[:])
}

func (rc *Receiver) validToken(token, hostname string) bool {
	match := false
	for _, t := range rc.Config.Tokens {
		if equal(token, t) {
			match = true
		}
	}
	for _, s := range rc.Config.Secrets {
		if equal(token, hostToken(s, hostname)) {
			match = true
		}
	}
	return match
}

func client(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	return r.RemoteAddr
}

func (rc *Receiver) fail(w http.ResponseWriter, r *http.Request, code int, body, reason string) {
	log.Printf("reject client=%s host=%q code=%d reason=%s", client(r), r.FormValue("hostname"), code, reason)
	w.WriteHeader(code)
	if body != "" {
		io.WriteString(w, body)
	}
}

func (rc *Receiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != endpoint && !strings.HasPrefix(r.URL.Path, endpoint+"/") {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, rc.MaxBody)
	if err := r.ParseMultipartForm(rc.MaxBody); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			rc.fail(w, r, http.StatusRequestEntityTooLarge, "", "body-too-large")
			return
		}
		rc.fail(w, r, http.StatusBadRequest, "", "form")
		return
	}
	defer r.MultipartForm.RemoveAll()

	token := r.FormValue("token")
	if token == "" {
		rc.fail(w, r, http.StatusNotAcceptable, "", "token-missing")
		return
	}
	hostname := r.FormValue("hostname")
	sum := r.FormValue("md5")
	if hostname == "" || sum == "" {
		rc.fail(w, r, http.StatusMethodNotAllowed, "", "hostname-or-md5-missing")
		return
	}
	if !validHostname(hostname) {
		rc.fail(w, r, http.StatusForbidden, "FAIL:HOSTNAME", "hostname")
		return
	}
	if !rc.validToken(token, hostname) {
		rc.fail(w, r, http.StatusNotFound, "", "token")
		return
	}
	payload, _, err := r.FormFile("payload")
	if err != nil {
		rc.fail(w, r, http.StatusBadRequest, "", "payload-missing")
		return
	}
	defer payload.Close()
	if err := rc.store(hostname, sum, payload); err != nil {
		rc.fail(w, r, http.StatusForbidden, "FAIL:WRITE", err.Error())
		return
	}
}

func (rc *Receiver) store(hostname, sum string, payload io.Reader) error {
	zr, err := gzip.NewReader(payload)
	if err != nil {
		return errors.New("gzip")
	}
	data, err := io.ReadAll(io.LimitReader(zr, rc.MaxPayload+1))
	if err != nil {
		return errors.New("gzip")
	}
	if int64(len(data)) > rc.MaxPayload {
		return errors.New("payload-too-large")
	}
	digest := md5.Sum(data)
	if hex.EncodeToString(digest[:]) != sum {
		return errors.New("md5")
	}
	tmp, err := os.CreateTemp(rc.Storage, ".check:"+hostname+":*")
	if err != nil {
		return errors.New("create")
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return errors.New("write")
	}
	if err := tmp.Chmod(0o640); err != nil {
		tmp.Close()
		return errors.New("chmod")
	}
	if err := tmp.Close(); err != nil {
		return errors.New("close")
	}
	if err := os.Rename(name, filepath.Join(rc.Storage, hostname)); err != nil {
		return errors.New("rename")
	}
	return nil
}
