package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/taekwondodev/lavagna/internal/round"
)

// The calls of the five-call walk in "What does the agent send on each call
// after the first?" (#15), with demonstrative content.
const (
	walkFirst = `::: phase Archivio delle sessioni
:::
# Dove salviamo lo stato? {id="storage"}
## Capire
Lo stato vive in un solo file.
## Decidere
- [file] Un file per sessione {recommended}
- [db] Database locale
Nessuna dipendenza nuova.

# Quanto teniamo le sessioni chiuse? {id="retention"}
## Decidere
- [day] 1 giorno di inattività {recommended}
- [week] 7 giorni
L'inattività riparte a ogni uso.

# Cosa succede se una sessione crasha? {id="crash"}
## Decidere
- [atomic] File temporaneo + rename {recommended}
- [none] Nessuna protezione
Poche righe, formato invariato.

# Chi esegue la pulizia? {id="cleanup" after="retention"}
`
	walkReply = `::: reply retention
Il giorno si conta dall'ultimo uso: se riprendi entro 24 ore la sessione resta.
:::
`
	walkRewrite = `# Quanto teniamo le sessioni chiuse? {id="retention"}
## Decidere
- [day] 1 giorno di inattività
- [week] 7 giorni {recommended}
Una settimana copre il weekend.

::: reply retention
Hai ragione sul weekend: ora consiglio 7 giorni.
:::
`
	walkAdvance = `::: settled storage
Nessuna dipendenza nuova e nessuna attesa tra sessioni.
:::
::: settled retention
Copre il weekend senza far crescere troppo il disco.
:::
# Chi esegue la pulizia? {id="cleanup" after="retention"}
## Decidere
- [sweep] Lo sweep all'avvio {recommended}
- [manual] Un comando manuale
`
	walkConfirm = `::: settled crash
Poche righe e nessun cambio di formato.
:::
::: settled cleanup
Nessun comando da ricordare.
:::
# Confermi le decisioni? {id="confirm"}
## Capire
::: recap
:::
::: evidence
- [unverified] Lo sweep non è misurato su dischi lenti.
:::
## Decidere
- [yes] Sì, confermo {recommended}
- [fix] No, correggo nella discussione
`
)

type pageView struct {
	Round    string `json:"round"`
	Token    string `json:"token"`
	Previous *struct {
		Call, Round, Submission, End string
	} `json:"previous"`
	Phase struct {
		Title     string `json:"title"`
		Questions []struct {
			ID      string `json:"id"`
			Planned bool   `json:"planned"`
			Frame   string `json:"frame"`
		} `json:"questions"`
	} `json:"phase"`
}

func (c *call) page() pageView {
	c.t.Helper()
	resp, err := http.Get(c.url + "events")
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(nil, 32<<20)
	for sc.Scan() {
		if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
			var v pageView
			if err := json.Unmarshal([]byte(data), &v); err != nil {
				c.t.Fatal(err)
			}
			return v
		}
	}
	c.t.Fatal("no round event")
	return pageView{}
}

func (c *call) get(path string) string {
	c.t.Helper()
	resp, err := http.Get(c.origin + path)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		c.t.Fatalf("GET %s: %d", path, resp.StatusCode)
	}
	return string(b)
}

// answer sends one batch to the live call and returns its stdout outcome.
func (c *call) answer(submission, questions string) string {
	c.t.Helper()
	v := c.page()
	body := fmt.Sprintf(`{"round":%q,"token":%q,"submission":%q,"questions":%s}`, v.Round, v.Token, submission, questions)
	if status := c.post(c.url+"send", c.origin, "application/json", body); status != http.StatusAccepted {
		c.t.Fatalf("send %s: %d", questions, status)
	}
	lines, code := c.finish()
	if code != 0 || len(lines) != 1 {
		c.t.Fatalf("outcome: exit %d %q", code, lines)
	}
	return lines[0]
}

func stateDir(t *testing.T, environ []string) string {
	t.Helper()
	home := ""
	for _, kv := range environ {
		if v, ok := strings.CutPrefix(kv, "HOME="); ok {
			home = v
		}
	}
	for _, pattern := range []string{"Library/Caches/lavagna/*/lock", ".cache/lavagna/*/lock"} {
		if found, _ := filepath.Glob(filepath.Join(home, pattern)); len(found) == 1 {
			return filepath.Dir(found[0])
		}
	}
	t.Fatal("no conversation directory")
	return ""
}

type ledgerState struct {
	Format int `json:"format"`
	Origin struct {
		Port int    `json:"port"`
		Cap  string `json:"cap"`
	} `json:"origin"`
	Ledger struct {
		Round     int `json:"round"`
		Questions map[string]struct {
			Status  string `json:"status"`
			Round   int    `json:"round"`
			Version int    `json:"version"`
			Bytes   int    `json:"bytes"`
			Marks   []struct {
				Round int    `json:"round"`
				Mark  string `json:"mark"`
			} `json:"marks"`
		} `json:"questions"`
		Decisions []struct {
			Question, Decision, Why string
			Round                   int
		} `json:"decisions"`
	} `json:"ledger"`
}

func readState(t *testing.T, dir string) ledgerState {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var s ledgerState
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func artifacts(t *testing.T, dir string) []string {
	t.Helper()
	entries, _ := os.ReadDir(filepath.Join(dir, "artifacts", "question"))
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestFiveCallWalkAcrossAGrillingPhase(t *testing.T) {
	environ := env(t, "LAVAGNA_SESSION=walk")

	first := startRound(t, environ, walkFirst)
	view := first.page()
	var ids []string
	for _, q := range view.Phase.Questions {
		ids = append(ids, q.ID)
	}
	if !strings.Contains(first.first, "round r1") || view.Phase.Title != "Archivio delle sessioni" || !slices.Equal(ids, []string{"storage", "retention", "crash", "cleanup"}) {
		t.Fatalf("first call: %q %+v", first.first, view.Phase)
	}
	got := first.answer("s-0000000000000001", `{"storage":{"choice":"file"},"retention":{"messages":["1 giorno non è poco?"]}}`)
	if want := `{"lavagna":"feedback","round":"r1","submission":"s-0000000000000001","questions":{"retention":{"messages":["1 giorno non è poco?"]},"storage":{"choice":"file"}}}`; got != want {
		t.Fatalf("call 1:\n%s\nwant\n%s", got, want)
	}

	reply := startRound(t, environ, walkReply)
	replyView := reply.page()
	if !strings.Contains(reply.first, "round r1") || replyView.Token == view.Token || replyView.Phase.Questions[0].Frame == view.Phase.Questions[0].Frame || len(replyView.Phase.Questions) != 4 {
		t.Fatalf("a reply-only call stays in r1 with a new token and frame key: %q %+v", reply.first, replyView)
	}
	got = reply.answer("s-0000000000000002", `{"storage":{"choice":"file"},"retention":{"messages":["Il weekend però sì: preferisco più margine."]}}`)
	if want := `{"lavagna":"feedback","round":"r1","submission":"s-0000000000000002","questions":{"retention":{"messages":["Il weekend però sì: preferisco più margine."]},"storage":{"choice":"file"}}}`; got != want {
		t.Fatalf("call 2: %s", got)
	}

	rewrite := startRound(t, environ, walkRewrite)
	if !strings.Contains(rewrite.first, "round r1") {
		t.Fatalf("a replacement stays in r1: %q", rewrite.first)
	}
	got = rewrite.answer("s-0000000000000003", `{"storage":{"choice":"file"},"retention":{"choice":"week"}}`)
	if want := `{"lavagna":"feedback","round":"r1","submission":"s-0000000000000003","questions":{"retention":{"choice":"week"},"storage":{"choice":"file"}}}`; got != want {
		t.Fatalf("call 3: %s", got)
	}

	advance := startRound(t, environ, walkAdvance)
	if !strings.Contains(advance.first, "round r2") {
		t.Fatalf("a new question opens r2: %q", advance.first)
	}
	frames := map[string]string{}
	for _, q := range advance.page().Phase.Questions {
		frames[q.ID] = q.Frame
	}
	if frames["crash"] != "" {
		t.Fatalf("a question with only 03 needs no frame: %q", frames["crash"])
	}
	if doc := advance.get(frames["storage"]); !strings.Contains(doc, "Lo stato vive in un solo file.") {
		t.Fatalf("a question from an earlier call is served from its artifact: %s", doc)
	}
	v := advance.page()
	settledChoice := fmt.Sprintf(`{"round":%q,"token":%q,"submission":"s-00000000000000ff","questions":{"storage":{"choice":"db"}}}`, v.Round, v.Token)
	if status := advance.post(advance.url+"send", advance.origin, "application/json", settledChoice); status != http.StatusBadRequest {
		t.Fatalf("a choice on a settled question: %d", status)
	}
	got = advance.answer("s-0000000000000004", `{"crash":{"choice":"atomic"},"cleanup":{"choice":"sweep"}}`)
	if want := `{"lavagna":"feedback","round":"r2","submission":"s-0000000000000004","questions":{"cleanup":{"choice":"sweep"},"crash":{"choice":"atomic"}}}`; got != want {
		t.Fatalf("call 4: %s", got)
	}
	dir := stateDir(t, environ)
	s := readState(t, dir)
	if crash := s.Ledger.Questions["crash"]; crash.Round != 2 || len(crash.Marks) != 1 || crash.Marks[0].Round != 1 || crash.Marks[0].Mark != "moved" {
		t.Fatalf("crash moves into r2 and r1 marks it moved: %+v", crash)
	}

	confirm := startRound(t, environ, walkConfirm)
	if !strings.Contains(confirm.first, "round r3") {
		t.Fatalf("the confirmation opens r3: %q", confirm.first)
	}
	frames = map[string]string{}
	for _, q := range confirm.page().Phase.Questions {
		frames[q.ID] = q.Frame
	}
	recap := confirm.get(frames["confirm"])
	for _, want := range []string{"Un file per sessione", "7 giorni", "File temporaneo + rename", "Lo sweep all&#39;avvio", "Copre il weekend senza far crescere troppo il disco.", "Database locale", `<td data-label="Round">r2</td>`} {
		if !strings.Contains(recap, want) {
			t.Fatalf("recap lacks %q:\n%s", want, recap)
		}
	}
	got = confirm.answer("s-0000000000000005", `{"confirm":{"choice":"yes"}}`)
	if want := `{"lavagna":"feedback","round":"r3","submission":"s-0000000000000005","questions":{"confirm":{"choice":"yes"}}}`; got != want {
		t.Fatalf("call 5: %s", got)
	}

	s = readState(t, dir)
	if s.Format != 1 || s.Ledger.Round != 3 || len(s.Ledger.Decisions) != 4 {
		t.Fatalf("final ledger: %+v", s.Ledger)
	}
	if names := artifacts(t, dir); !slices.Equal(names, []string{"cleanup-1.json", "confirm-1.json", "crash-1.json", "retention-2.json", "storage-1.json"}) {
		t.Fatalf("only current versions remain: %v", names)
	}
	for id, q := range s.Ledger.Questions {
		b, err := os.ReadFile(filepath.Join(dir, "artifacts", "question", fmt.Sprintf("%s-%d.json", id, q.Version)))
		var stored struct {
			Question  round.PhaseQuestion `json:"question"`
			HTML      string              `json:"html"`
			Resources []round.File        `json:"resources"`
		}
		if err != nil || json.Unmarshal(b, &stored) != nil {
			t.Fatalf("%s: %v", id, err)
		}
		version := stored.Question
		version.HTML, version.Resources = stored.HTML, stored.Resources
		rendered := version.Size()
		if rendered == 0 || rendered != q.Bytes {
			t.Fatalf("%s records %d bytes for a version rendered in %d", id, q.Bytes, rendered)
		}
	}

	if lines, code := run(t, environ, "", "close"); code != 0 {
		t.Fatalf("close: %d %q", code, lines)
	}
	for _, src := range []string{walkReply, "::: settled storage\n:::\n"} {
		lines, code := run(t, environ, src, "round")
		if code != 2 || !strings.Contains(last(lines), "send complete questions") {
			t.Fatalf("after close: %d %q", code, lines)
		}
	}
}

func TestInvalidCallLeavesStateAndArtifactsAndPrunesOrphans(t *testing.T) {
	environ := env(t, "LAVAGNA_SESSION=orphans")
	startRound(t, environ, decision).answer("s-0000000000000001", `{"storage":{"choice":"db"}}`)
	dir := stateDir(t, environ)
	before, _ := os.ReadFile(filepath.Join(dir, "state.json"))
	listed := artifacts(t, dir)
	for _, src := range []string{
		"::: settled storage sqlite\n:::\n",
		"::: reply ghost\nx\n:::\n# Nuova {id=\"next\"}\n## Decidere\n- [a] A\n- [b] B\n",
		"# Dove salviamo lo stato? {id=\"storage\"}\n",
	} {
		lines, code := run(t, environ, src, "round")
		after, _ := os.ReadFile(filepath.Join(dir, "state.json"))
		if code != 2 || !bytes.Equal(before, after) || !slices.Equal(listed, artifacts(t, dir)) {
			t.Fatalf("invalid call %q: exit %d %q; state or artifacts changed", src, code, lines)
		}
	}

	// A call that died before its commit leaves versions the ledger never named.
	for _, name := range []string{"storage-2.json", "ghost-1.json", ".artifact-123.tmp"} {
		if err := os.WriteFile(filepath.Join(dir, "artifacts", "question", name), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	replaced := strings.Replace(decision, "Un database locale", "SQLite", 1)
	startRound(t, environ, replaced).answer("s-0000000000000002", `{"storage":{"choice":"file"}}`)
	if names := artifacts(t, dir); !slices.Equal(names, []string{"storage-2.json"}) {
		t.Fatalf("orphans and the replaced version are deleted: %v", names)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "artifacts", "question", "storage-2.json")); !bytes.Contains(b, []byte("SQLite")) {
		t.Fatalf("the orphan was not replaced by the committed version: %s", b)
	}
}

func TestCallsAreKeyedForInterruptedOutcomes(t *testing.T) {
	environ := env(t, "LAVAGNA_SESSION=interrupted")
	startRound(t, environ, decision).answer("s-0000000000000001", `{"storage":{"choice":"db"}}`)
	stopped := startRound(t, environ, "::: reply storage\nCi penso.\n:::\n")
	stopped.esc()
	next := startRound(t, environ, "")
	v := next.page()
	if !strings.Contains(next.first, "round r1") || v.Previous == nil || v.Previous.Call != "c2" || v.Previous.Round != "r1" || v.Previous.End != "interrupted" {
		t.Fatalf("an empty call resumes waiting in r1 after an interrupted call: %q %+v", next.first, v.Previous)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestStateFromAnotherVersion(t *testing.T) {
	t.Run("an older format restarts the phase on the same origin", func(t *testing.T) {
		environ := env(t, "LAVAGNA_SESSION=older")
		if lines, code := run(t, environ, "", "close"); code != 0 {
			t.Fatalf("close: %q", lines)
		}
		dir := stateDir(t, environ)
		port, cap := freePort(t), strings.Repeat("ab", 32)
		old := fmt.Sprintf(`{"origin":{"port":%d,"cap":%q},"rounds":3,"live":"r3","anchors":["Old"]}`, port, cap)
		writeFiles(t, dir, map[string]string{"state.json": old, "artifacts/round/r3.json": "{}", "artifacts/feedback/s-00000001.json": "{}", "artifacts/question/old-1.json": "{}", "images/0123.png": "png"})

		cmd := command(environ, decision)
		stdout, _ := cmd.StdoutPipe()
		stderr, _ := cmd.StderrPipe()
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		c := &call{t: t, cmd: cmd, out: bufio.NewReader(stdout)}
		t.Cleanup(c.esc)
		status := bufio.NewReader(stderr)
		notice, _ := status.ReadString('\n')
		first, _ := status.ReadString('\n')
		go io.Copy(io.Discard, status)
		c.status(first)
		if want := fmt.Sprintf("http://127.0.0.1:%d/s/%s/", port, cap); c.url != want || !strings.Contains(notice, "older lavagna") || !strings.Contains(c.first, "round r1") {
			t.Fatalf("restart notice and origin: %q %q, url %s", notice, first, c.url)
		}
		c.answer("s-0000000000000001", `{"storage":{"choice":"db"}}`)
		for _, gone := range []string{"artifacts/round", "artifacts/feedback/s-00000001.json", "artifacts/question/old-1.json", "images/0123.png"} {
			if _, err := os.Stat(filepath.Join(dir, gone)); !os.IsNotExist(err) {
				t.Fatalf("old phase data %s survives: %v", gone, err)
			}
		}
		if s := readState(t, dir); s.Format != 1 || s.Origin.Port != port || s.Origin.Cap != cap || s.Ledger.Round != 1 {
			t.Fatalf("fresh phase state: %+v", s)
		}
	})
	t.Run("a newer format is refused untouched until close", func(t *testing.T) {
		environ := env(t, "LAVAGNA_SESSION=newer")
		run(t, environ, "", "close")
		dir := stateDir(t, environ)
		newer := `{"format":2,"origin":{"port":1,"cap":"x"},"ledger":["unknown"]}`
		writeFiles(t, dir, map[string]string{"state.json": newer, "artifacts/question/q-9.json": "{}"})
		lines, code := run(t, environ, decision, "round")
		if code != 1 || !strings.Contains(last(lines), "newer lavagna") || !strings.Contains(last(lines), "lavagna close") {
			t.Fatalf("round on a newer format: %d %q", code, lines)
		}
		if b, _ := os.ReadFile(filepath.Join(dir, "state.json")); string(b) != newer || !slices.Equal(artifacts(t, dir), []string{"q-9.json"}) {
			t.Fatalf("newer state was touched: %s", b)
		}
		if lines, code := run(t, environ, "", "close"); code != 0 || !strings.Contains(last(lines), `"closed"`) {
			t.Fatalf("close on a newer format: %d %q", code, lines)
		}
		if _, err := os.Stat(filepath.Join(dir, "state.json")); !os.IsNotExist(err) {
			t.Fatalf("close left state: %v", err)
		}
	})
}
