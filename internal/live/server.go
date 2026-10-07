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
	"net/url"
	"os"
	"path"
	"path/filepath"
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
	maxImages       = 8
	maxImageBytes   = 10 << 20
	retryMillis     = 250
	inactive        = "Questo round non è più attivo."
)

const (
	shellCSP = "default-src 'none'; script-src 'self' data:; style-src 'self' data:; font-src 'self' data:; img-src 'self' data:; connect-src 'self'; frame-src 'self' blob:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
	frameCSP = "sandbox allow-scripts; default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors 'self'"
)

var submissionPattern = regexp.MustCompile(`^s-[0-9a-f]{8,64}$`)

var imageExtensions = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/webp": ".webp",
	"image/gif":  ".gif",
}

type stage int

const (
	pending stage = iota
	accepted
	returned
	received
	unread
	ended
	aborted
	unreadAborted
)

func (s stage) MarshalText() ([]byte, error) {
	return []byte([...]string{"", "accepted", "returned", "received", "unread", "ended", "aborted", "unread-aborted"}[s]), nil
}

type view struct {
	ID         string                `json:"round"`
	Token      string                `json:"token"`
	Limit      int                   `json:"limit"`
	Frame      string                `json:"frame"`
	Resources  []string              `json:"resources"`
	ImageLimit int                   `json:"imageLimit"`
	ImageBytes int                   `json:"imageBytes"`
	Previous   *conversation.Outcome `json:"previous"`
	Phase      *round.Phase          `json:"phase,omitempty"`
	round.Round
}

type frame struct {
	key   string
	doc   []byte
	files map[string]round.File
}

type receipt struct {
	Submission  string `json:"submission"`
	Stage       stage  `json:"stage"`
	Unwitnessed bool   `json:"unwitnessed,omitempty"`
}

type phaseFeedback struct {
	Choice   string
	Answer   string
	Messages []string
	Images   []string
}

type batch struct {
	round      string
	submission string
	choices    map[string]string
	comments   []comment
	images     []string
	questions  map[string]phaseFeedback
	overview   phaseFeedback
}

type phaseQuestionLine struct {
	Choice   string `json:"choice,omitempty"`
	Answer   any    `json:"answer,omitempty"`
	Messages any    `json:"messages,omitempty"`
	Images   any    `json:"images,omitempty"`
}

type phaseOutcomeLine struct {
	Lavagna    string                       `json:"lavagna"`
	Round      string                       `json:"round"`
	Submission string                       `json:"submission"`
	Deferred   bool                         `json:"deferred,omitempty"`
	Questions  map[string]phaseQuestionLine `json:"questions"`
	Overview   *phaseQuestionLine           `json:"overview,omitempty"`
}

func (b batch) phaseLine() phaseOutcomeLine {
	questions := map[string]phaseQuestionLine{}
	for id, f := range b.questions {
		line := phaseQuestionLine{Choice: f.Choice}
		if f.Answer != "" {
			line.Answer = f.Answer
		}
		if len(f.Messages) > 0 {
			line.Messages = f.Messages
		}
		if len(f.Images) > 0 {
			line.Images = f.Images
		}
		questions[id] = line
	}
	var overview *phaseQuestionLine
	if len(b.overview.Messages) > 0 || len(b.overview.Images) > 0 {
		overview = &phaseQuestionLine{}
		if len(b.overview.Messages) > 0 {
			overview.Messages = b.overview.Messages
		}
		if len(b.overview.Images) > 0 {
			overview.Images = b.overview.Images
		}
	}
	return phaseOutcomeLine{Lavagna: "feedback", Round: b.round, Submission: b.submission, Questions: questions, Overview: overview}
}

func (b batch) deferredPhaseLine() phaseOutcomeLine {
	questions := map[string]phaseQuestionLine{}
	for id, f := range b.questions {
		line := phaseQuestionLine{Choice: f.Choice}
		if f.Answer != "" {
			line.Answer = true
		}
		if len(f.Messages) > 0 {
			line.Messages = len(f.Messages)
		}
		if len(f.Images) > 0 {
			line.Images = len(f.Images)
		}
		questions[id] = line
	}
	var overview *phaseQuestionLine
	if len(b.overview.Messages) > 0 || len(b.overview.Images) > 0 {
		overview = &phaseQuestionLine{}
		if len(b.overview.Messages) > 0 {
			overview.Messages = len(b.overview.Messages)
		}
		if len(b.overview.Images) > 0 {
			overview.Images = len(b.overview.Images)
		}
	}
	return phaseOutcomeLine{Lavagna: "feedback", Round: b.round, Submission: b.submission, Deferred: true, Questions: questions, Overview: overview}
}

type upload struct {
	path        string
	contentType string
}

type comment struct {
	Anchor *string `json:"anchor"`
	Text   string  `json:"text"`
}

type server struct {
	origin      conversation.Origin
	mu          sync.Mutex
	gate        gate
	view        *view
	frame       *frame
	frames      map[string]*frame
	options     map[string]map[string]bool
	anchors     map[string]bool
	imageDir    string
	uploads     map[string]upload
	stage       stage
	unwitnessed bool
	streams     map[chan event]struct{}
	accepted    chan batch
	seen        chan struct{}
	seenOnce    sync.Once
	done        chan struct{}
	stopOnce    sync.Once
	closeToken  string
	cleaned     chan struct{}
	cleanOnce   sync.Once
}

type event struct {
	name string
	data any
}

type roundSpec struct {
	Origin   conversation.Origin
	ID       string
	Token    string
	FrameKey string
	Round    round.Round
	Files    []round.File
	Previous *conversation.Outcome
	Images   string
	Phase    *round.Phase
}

func newRound(spec roundSpec) *server {
	r := spec.Round
	s := newServer(spec.Origin)
	s.view = &view{ID: spec.ID, Token: spec.Token, Limit: maxCommentBytes, ImageLimit: maxImages, ImageBytes: maxImageBytes, Previous: spec.Previous, Round: r, Phase: spec.Phase}
	s.imageDir = spec.Images
	s.loadUploads()
	s.gate = gate{cap: spec.Origin.Cap, round: spec.ID, token: spec.Token}
	if spec.Phase != nil {
		s.frames = map[string]*frame{}
		for i := range spec.Phase.Questions {
			q := &spec.Phase.Questions[i]
			if q.Planned {
				continue
			}
			files := map[string]round.File{}
			var names []string
			for _, file := range q.Resources {
				files[file.Name] = file
				names = append(names, "../"+q.ID+"/"+file.Name)
			}
			f := &frame{key: spec.FrameKey + q.ID, files: files, doc: page.Frame(q.HTML, names)}
			q.FrameKey = f.key
			q.Frame = "/f/" + f.key + "/" + q.ID + "/"
			s.frames[q.ID] = f
		}
	} else if r.Content != "" {
		f := &frame{key: spec.FrameKey, files: map[string]round.File{}}
		var names []string
		for _, file := range spec.Files {
			f.files[file.Name] = file
			names = append(names, file.Name)
		}
		f.doc = page.Frame(r.Content, names)
		s.frame = f
		s.view.Frame = "/f/" + f.key + "/"
		for _, name := range append(names, "", page.FrameAsset+"lavagna.css", page.FrameAsset+"frame.js", page.FrameAsset+page.Font) {
			s.view.Resources = append(s.view.Resources, s.view.Frame+(&url.URL{Path: name}).EscapedPath())
		}
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

func (s *server) loadUploads() {
	entries, err := os.ReadDir(s.imageDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !submissionPattern.MatchString("s-"+strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))) {
			continue
		}
		ext := filepath.Ext(entry.Name())
		contentType := map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".webp": "image/webp", ".gif": "image/gif"}[ext]
		if contentType == "" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ext)
		s.uploads[id] = upload{path: filepath.Join(s.imageDir, entry.Name()), contentType: contentType}
	}
}

func newClose(o conversation.Origin) *server {
	s := newServer(o)
	s.closeToken = conversation.Secret(32)
	s.cleaned = make(chan struct{})
	return s
}

func newServer(o conversation.Origin) *server {
	return &server{
		origin:   o,
		options:  map[string]map[string]bool{},
		anchors:  map[string]bool{},
		uploads:  map[string]upload{},
		streams:  map[chan event]struct{}{},
		accepted: make(chan batch, 1),
		seen:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

func (s *server) returned(witnessed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stage = returned
	s.unwitnessed = !witnessed
	s.broadcast(event{"receipt", s.receipt()})
}

func (s *server) advance(st stage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stage = max(s.stage, st)
	s.broadcast(event{"receipt", s.receipt()})
}

func (s *server) unwitness() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unwitnessed = true
	s.broadcast(event{"receipt", s.receipt()})
}

func (s *server) watched() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.streams) > 0
}

func (s *server) stop() { s.stopOnce.Do(func() { close(s.done) }) }

func (s *server) receipt() receipt { return receipt{s.gate.admitted, s.stage, s.unwitnessed} }

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
	mux.HandleFunc("GET /s/{cap}/sw.js", s.capable(s.worker))
	mux.HandleFunc("GET /s/{cap}/assets/{file...}", s.capable(s.asset))
	mux.HandleFunc("GET /s/{cap}/events", s.capable(s.events))
	mux.HandleFunc("POST /s/{cap}/close-ack", s.capable(s.closeAck))
	mux.HandleFunc("POST /s/{cap}/send", s.send)
	mux.HandleFunc("GET /f/{key}/{file...}", s.framed)
	mux.HandleFunc("POST /s/{cap}/images", s.capable(s.attachImage))
	mux.HandleFunc("GET /s/{cap}/images/{id}", s.capable(s.image))
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

func (s *server) worker(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", page.ContentType("sw.js"))
	w.Write(page.Worker)
}

func (s *server) asset(w http.ResponseWriter, r *http.Request) {
	serveAsset(w, r, r.PathValue("file"))
}

func (s *server) framed(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSuffix(r.PathValue("file"), "/")
	var f *frame
	if s.frames != nil {
		question, rest, found := strings.Cut(name, "/")
		if !found {
			question, name = name, ""
		} else {
			name = rest
		}
		f = s.frames[question]
		if strings.HasPrefix(name, question+"/") {
			name = strings.TrimPrefix(name, question+"/")
		}
	} else {
		f = s.frame
	}
	if f == nil || !same(r.PathValue("key"), f.key) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Security-Policy", frameCSP)
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
	first := []event{{"closed", map[string]string{"token": s.closeToken}}}
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

func (s *server) closeAck(w http.ResponseWriter, r *http.Request) {
	if s.closeToken == "" || r.Header.Get("Origin") != "http://"+s.origin.Host() {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil || !same(body.Token, s.closeToken) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	s.cleanOnce.Do(func() { close(s.cleaned) })
	w.WriteHeader(http.StatusNoContent)
}

func writeEvent(w io.Writer, e event) error {
	b, err := json.Marshal(e.data)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.name, b)
	return err
}

type sendQuestion struct {
	Choice   string   `json:"choice"`
	Answer   string   `json:"answer"`
	Messages []string `json:"messages"`
	Images   []string `json:"images"`
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
	Images    []string                `json:"images"`
	Questions map[string]sendQuestion `json:"questions"`
	Overview  sendQuestion            `json:"overview"`
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

func (s *server) fromPage(w http.ResponseWriter, r *http.Request, mediaType string) bool {
	if r.Header.Get("Origin") != "http://"+s.origin.Host() {
		refuse(w, http.StatusForbidden, "origin")
		return false
	}
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != mediaType {
		refuse(w, http.StatusUnsupportedMediaType, "content type")
		return false
	}
	return true
}

func (s *server) send(w http.ResponseWriter, r *http.Request) {
	if !s.fromPage(w, r, "application/json") {
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
	if s.view.Phase != nil {
		return s.validatePhase(in)
	}
	b := batch{round: in.Round, submission: in.Submission, choices: map[string]string{}, comments: []comment{}, images: []string{}}
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
	if len(in.Images) > maxImages {
		return b, http.StatusRequestEntityTooLarge, fmt.Sprintf("more than %d images", maxImages)
	}
	attached := map[string]bool{}
	for _, id := range in.Images {
		u, ok := s.uploads[id]
		if !ok || attached[id] {
			return b, http.StatusBadRequest, "unknown image"
		}
		attached[id] = true
		b.images = append(b.images, u.path)
	}
	if len(b.choices) == 0 && len(b.comments) == 0 && len(b.images) == 0 {
		return b, http.StatusBadRequest, "empty batch"
	}
	if encodedLen(b.line()) > maxResultBytes {
		return b, http.StatusRequestEntityTooLarge, fmt.Sprintf("feedback exceeds %d bytes", maxResultBytes)
	}
	return b, 0, ""
}

func (s *server) validatePhase(in sendBody) (batch, int, string) {
	b := batch{round: in.Round, submission: in.Submission, questions: map[string]phaseFeedback{}}
	if len(in.Choices) > 0 || len(in.Comments) > 0 || len(in.Images) > 0 {
		return b, http.StatusBadRequest, "legacy feedback fields are not supported"
	}
	known := map[string]map[string]bool{}
	for _, q := range s.view.Phase.Questions {
		opts := map[string]bool{}
		for _, o := range q.Options {
			opts[o.ID] = true
		}
		known[q.ID] = opts
	}
	totalText := 0
	attached := map[string]bool{}
	useImages := func(ids []string) ([]string, string) {
		paths := []string{}
		for _, id := range ids {
			u, ok := s.uploads[id]
			if !ok || attached[id] {
				return nil, "unknown image"
			}
			attached[id] = true
			paths = append(paths, u.path)
		}
		return paths, ""
	}
	for id, item := range in.Questions {
		options, ok := known[id]
		if !ok {
			return b, http.StatusBadRequest, "unknown question"
		}
		if item.Choice != "" && item.Answer != "" {
			return b, http.StatusBadRequest, "a question cannot have both choice and answer"
		}
		if item.Choice != "" && !options[item.Choice] {
			return b, http.StatusBadRequest, "unknown choice"
		}
		if item.Answer != "" {
			totalText += encodedLen(item.Answer) - 2
		}
		f := phaseFeedback{Choice: item.Choice, Answer: item.Answer, Messages: []string{}}
		for _, text := range item.Messages {
			if strings.TrimSpace(text) == "" {
				return b, http.StatusBadRequest, "empty message"
			}
			totalText += encodedLen(text) - 2
			f.Messages = append(f.Messages, text)
		}
		paths, problem := useImages(item.Images)
		if problem != "" {
			return b, http.StatusBadRequest, problem
		}
		f.Images = paths
		if f.Choice != "" || f.Answer != "" || len(f.Messages) > 0 || len(f.Images) > 0 {
			b.questions[id] = f
		}
	}
	if in.Overview.Choice != "" || in.Overview.Answer != "" {
		return b, http.StatusBadRequest, "overview accepts messages and images only"
	}
	b.overview.Messages = []string{}
	for _, text := range in.Overview.Messages {
		if strings.TrimSpace(text) == "" {
			return b, http.StatusBadRequest, "empty message"
		}
		totalText += encodedLen(text) - 2
		b.overview.Messages = append(b.overview.Messages, text)
	}
	paths, problem := useImages(in.Overview.Images)
	if problem != "" {
		return b, http.StatusBadRequest, problem
	}
	b.overview.Images = paths
	if totalText > maxCommentBytes {
		return b, http.StatusRequestEntityTooLarge, fmt.Sprintf("feedback text exceeds %d bytes", maxCommentBytes)
	}
	countImages := len(b.overview.Images)
	for _, f := range b.questions {
		countImages += len(f.Images)
	}
	if countImages > maxImages {
		return b, http.StatusRequestEntityTooLarge, fmt.Sprintf("more than %d images", maxImages)
	}
	if len(b.questions) == 0 && len(b.overview.Messages) == 0 && len(b.overview.Images) == 0 {
		return b, http.StatusBadRequest, "empty batch"
	}
	if encodedLen(b.phaseLine()) > maxResultBytes {
		return b, http.StatusRequestEntityTooLarge, fmt.Sprintf("feedback exceeds %d bytes", maxResultBytes)
	}
	return b, 0, ""
}

func encodedLen(v any) int {
	var buf bytes.Buffer
	encodeJSON(&buf, v)
	return buf.Len() - len("\n")
}

func (s *server) attachImage(w http.ResponseWriter, r *http.Request) {
	if !s.fromPage(w, r, "application/octet-stream") || !s.open(w, r) {
		return
	}
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxImageBytes))
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		refuse(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("image exceeds %d bytes", maxImageBytes))
		return
	case err != nil:
		refuse(w, http.StatusBadRequest, "malformed image")
		return
	}
	contentType := http.DetectContentType(b)
	ext, ok := imageExtensions[contentType]
	if !ok {
		refuse(w, http.StatusUnsupportedMediaType, "not a PNG, JPEG, WebP or GIF image")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.openLocked(w, r) {
		return
	}
	id := conversation.Secret(16)
	path := filepath.Join(s.imageDir, id+ext)
	if err := store(path, b); err != nil {
		refuse(w, http.StatusInternalServerError, "image not stored")
		return
	}
	s.uploads[id] = upload{path: path, contentType: contentType}
	reply(w, http.StatusCreated, struct {
		Image string `json:"image"`
	}{id})
}

func (s *server) open(w http.ResponseWriter, r *http.Request) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.openLocked(w, r)
}

func (s *server) openLocked(w http.ResponseWriter, r *http.Request) bool {
	if s.view == nil || !s.gate.open(r.Header.Get("Lavagna-Round"), r.Header.Get("Lavagna-Token")) {
		refuse(w, http.StatusConflict, inactive)
		return false
	}
	return true
}

func store(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	return f.Close()
}

func (s *server) image(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	u, ok := s.uploads[r.PathValue("id")]
	s.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", u.contentType)
	http.ServeFile(w, r, u.path)
}
