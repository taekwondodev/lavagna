package live

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strings"
	"sync"

	"github.com/taekwondodev/lavagna/internal/conversation"
	"github.com/taekwondodev/lavagna/internal/page"
	"github.com/taekwondodev/lavagna/internal/round"
)

const (
	maxCommentBytes = 32 << 10
	maxResultBytes  = 48 << 10
	maxBody         = 256 << 10
	retryMillis     = 250
	inactive        = "Questo round non è più attivo."
)

const (
	shellCSP = "default-src 'none'; script-src 'self'; style-src 'self'; font-src 'self'; img-src 'self' data:; connect-src 'self'; frame-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
	frameCSP = "sandbox allow-scripts; default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors 'self'"
)

var submissionPattern = regexp.MustCompile(`^s-[0-9a-f]{8,64}$`)

type stage int

const (
	pending stage = iota
	accepted
	returned
)

func (s stage) MarshalText() ([]byte, error) {
	return []byte([...]string{"", "accepted", "returned"}[s]), nil
}

type view struct {
	ID    string `json:"round"`
	Token string `json:"token"`
	Limit int    `json:"limit"`
	Frame string `json:"frame"`
	round.Round
}

type frame struct {
	key   string
	doc   []byte
	files map[string]round.File
}

type receipt struct {
	Submission string `json:"submission"`
	Stage      stage  `json:"stage"`
}

type batch struct {
	round      string
	submission string
	choices    map[string]string
	comments   []comment
}

type comment struct {
	Anchor *string `json:"anchor"`
	Text   string  `json:"text"`
}

type server struct {
	origin   conversation.Origin
	mu       sync.Mutex
	gate     gate
	view     *view
	frame    *frame
	options  map[string]map[string]bool
	anchors  map[string]bool
	stage    stage
	streams  map[chan event]struct{}
	accepted chan batch
	seen     chan struct{}
	seenOnce sync.Once
	done     chan struct{}
	stopOnce sync.Once
}

type event struct {
	name string
	data any
}

func newRound(o conversation.Origin, id, token string, r round.Round, files []round.File) *server {
	s := newServer(o)
	s.view = &view{ID: id, Token: token, Limit: maxCommentBytes, Round: r}
	s.gate = gate{cap: o.Cap, round: id, token: token}
	if r.Content != "" {
		f := &frame{key: conversation.Secret(16), files: map[string]round.File{}}
		var names []string
		for _, file := range files {
			f.files[file.Name] = file
			names = append(names, file.Name)
		}
		f.doc = page.Frame(r.Content, names)
		s.frame = f
		s.view.Frame = "/f/" + f.key + "/"
	}
	for _, a := range r.Anchors {
		s.anchors[a] = true
	}
	for _, q := range r.Questions {
		s.options[q.ID] = map[string]bool{}
		for _, opt := range q.Options {
			s.options[q.ID][opt] = true
		}
	}
	return s
}

func newClose(o conversation.Origin) *server { return newServer(o) }

func newServer(o conversation.Origin) *server {
	return &server{
		origin:   o,
		options:  map[string]map[string]bool{},
		anchors:  map[string]bool{},
		streams:  map[chan event]struct{}{},
		accepted: make(chan batch, 1),
		seen:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

func (s *server) returned() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stage = returned
	s.broadcast(event{"receipt", s.receipt()})
}

func (s *server) stop() { s.stopOnce.Do(func() { close(s.done) }) }

func (s *server) receipt() receipt { return receipt{s.gate.admitted, s.stage} }

func (s *server) broadcast(e event) {
	for ch := range s.streams {
		select {
		case ch <- e:
		default:
		}
	}
}

func (s *server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /s/{cap}/{$}", s.capable(s.shell))
	mux.HandleFunc("GET /s/{cap}/assets/{file...}", s.capable(s.asset))
	mux.HandleFunc("GET /s/{cap}/events", s.capable(s.events))
	mux.HandleFunc("POST /s/{cap}/send", s.send)
	mux.HandleFunc("GET /f/{key}/{file...}", s.framed)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		if r.Host != s.origin.Host() {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (s *server) capable(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !same(r.PathValue("cap"), s.origin.Cap) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func (s *server) shell(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Security-Policy", shellCSP)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(page.Shell)
}

func (s *server) asset(w http.ResponseWriter, r *http.Request) {
	serveAsset(w, r, r.PathValue("file"))
}

func (s *server) framed(w http.ResponseWriter, r *http.Request) {
	f := s.frame
	if f == nil || !same(r.PathValue("key"), f.key) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Security-Policy", frameCSP)
	name := r.PathValue("file")
	if asset, ok := strings.CutPrefix(name, page.FrameAsset); ok {
		serveAsset(w, r, asset)
		return
	}
	if name == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(f.doc)
		return
	}
	file, ok := f.files[name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", round.Types[strings.ToLower(path.Ext(name))])
	w.Write(file.Body)
}

func serveAsset(w http.ResponseWriter, r *http.Request, name string) {
	b, err := fs.ReadFile(page.Assets, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if name == page.Font {
		w.Header().Set("Access-Control-Allow-Origin", "*")
	}
	w.Header().Set("Content-Type", page.ContentType(name))
	w.Write(b)
}

func (s *server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	ch := make(chan event, 8)
	s.mu.Lock()
	s.streams[ch] = struct{}{}
	first := []event{{"closed", struct{}{}}}
	if s.view != nil {
		first = []event{{"round", s.view}}
		if s.stage != pending {
			first = append(first, event{"receipt", s.receipt()})
		}
	}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.streams, ch)
		s.mu.Unlock()
	}()

	fmt.Fprintf(w, "retry: %d\n\n", retryMillis)
	for _, e := range first {
		if writeEvent(w, e) != nil {
			return
		}
	}
	flusher.Flush()
	s.seenOnce.Do(func() { close(s.seen) })
	for {
		select {
		case e := <-ch:
			if writeEvent(w, e) != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		case <-s.done:
			for {
				select {
				case e := <-ch:
					if writeEvent(w, e) != nil {
						return
					}
				default:
					flusher.Flush()
					return
				}
			}
		}
	}
}

func writeEvent(w io.Writer, e event) error {
	b, err := json.Marshal(e.data)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.name, b)
	return err
}

type sendBody struct {
	Round      string            `json:"round"`
	Token      string            `json:"token"`
	Submission string            `json:"submission"`
	Choices    map[string]string `json:"choices"`
	Comments   []struct {
		Text   string  `json:"text"`
		Anchor *string `json:"anchor"`
	} `json:"comments"`
}

func reply(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

func refuse(w http.ResponseWriter, status int, message string) {
	reply(w, status, struct {
		Error string `json:"error"`
	}{message})
}

func (s *server) send(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != "http://"+s.origin.Host() {
		refuse(w, http.StatusForbidden, "origin")
		return
	}
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		refuse(w, http.StatusUnsupportedMediaType, "content type")
		return
	}
	var in sendBody
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	err := dec.Decode(&in)
	if err == nil && dec.Decode(&json.RawMessage{}) != io.EOF {
		err = errors.New("trailing data")
	}
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		refuse(w, http.StatusRequestEntityTooLarge, "batch too large")
		return
	case err != nil, in.Round == "", in.Token == "", !submissionPattern.MatchString(in.Submission):
		refuse(w, http.StatusBadRequest, "malformed batch")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.view == nil {
		refuse(w, http.StatusConflict, inactive)
		return
	}
	g, v := s.gate.admit(send{cap: r.PathValue("cap"), round: in.Round, token: in.Token, submission: in.Submission})
	switch v {
	case duplicate:
		reply(w, http.StatusOK, s.receipt())
		return
	case answered, stale, foreign:
		refuse(w, http.StatusConflict, inactive)
		return
	}
	b, status, problem := s.validate(in)
	if problem != "" {
		refuse(w, status, problem)
		return
	}
	s.gate = g
	s.stage = accepted
	s.accepted <- b
	s.broadcast(event{"receipt", s.receipt()})
	reply(w, http.StatusAccepted, s.receipt())
}

func (s *server) validate(in sendBody) (batch, int, string) {
	b := batch{round: in.Round, submission: in.Submission, choices: map[string]string{}, comments: []comment{}}
	for q, opt := range in.Choices {
		if !s.options[q][opt] {
			return b, http.StatusBadRequest, "unknown choice"
		}
		b.choices[q] = opt
	}
	total := 0
	for _, c := range in.Comments {
		if strings.TrimSpace(c.Text) == "" {
			return b, http.StatusBadRequest, "empty comment"
		}
		if c.Anchor != nil && !s.anchors[*c.Anchor] {
			return b, http.StatusBadRequest, "unknown anchor"
		}
		total += encodedLen(c.Text) - len(`""`)
		b.comments = append(b.comments, comment{Anchor: c.Anchor, Text: c.Text})
	}
	if total > maxCommentBytes {
		return b, http.StatusRequestEntityTooLarge, fmt.Sprintf("comment text exceeds %d bytes", maxCommentBytes)
	}
	if len(b.choices) == 0 && len(b.comments) == 0 {
		return b, http.StatusBadRequest, "empty batch"
	}
	if encodedLen(b.line()) > maxResultBytes {
		return b, http.StatusRequestEntityTooLarge, fmt.Sprintf("feedback exceeds %d bytes", maxResultBytes)
	}
	return b, 0, ""
}

func encodedLen(v any) int {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
	return buf.Len() - len("\n")
}
