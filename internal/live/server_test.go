package live

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/taekwondodev/lavagna/internal/conversation"
	"github.com/taekwondodev/lavagna/internal/round"
)

func serve(t *testing.T) (*server, func(body string) int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	o := conversation.Origin{Port: ln.Addr().(*net.TCPAddr).Port, Cap: "cap-a"}
	r, errs := round.Parse([]byte("# Decidere\n## Dove? {id=\"storage\"}\n- [file] File\n- [db] Database\n"))
	if errs != nil {
		t.Fatal(errs)
	}
	s := newRound(o, "r1", "tok-1", r)
	hs := &http.Server{Handler: s.handler()}
	go hs.Serve(ln)
	t.Cleanup(func() { hs.Close() })
	post := func(body string) int {
		req, _ := http.NewRequest(http.MethodPost, o.URL()+"send", strings.NewReader(body))
		req.Header.Set("Origin", "http://"+o.Host())
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	return s, post
}

func sendBatch(submission, comment string) string {
	c, _ := json.Marshal(comment)
	return fmt.Sprintf(`{"round":"r1","token":"tok-1","submission":%q,"choices":{"storage":"db"},"comments":[{"text":%s}]}`, submission, c)
}

func TestSendResolvesADuplicateOnce(t *testing.T) {
	s, post := serve(t)
	for _, step := range []struct {
		submission string
		status     int
	}{
		{"s-00000000000000aa", http.StatusAccepted},
		{"s-00000000000000aa", http.StatusOK},
		{"s-00000000000000bb", http.StatusConflict},
	} {
		if status := post(sendBatch(step.submission, "ok")); status != step.status {
			t.Fatalf("%s: status %d, want %d", step.submission, status, step.status)
		}
	}
	if got := <-s.accepted; got.submission != "s-00000000000000aa" {
		t.Fatalf("accepted %q", got.submission)
	}
	select {
	case extra := <-s.accepted:
		t.Fatalf("a second batch was delivered: %q", extra.submission)
	default:
	}
}

func TestSendBoundsCommentTextAsTheResultEncodesIt(t *testing.T) {
	_, post := serve(t)
	if status := post(sendBatch("s-00000000000000aa", strings.Repeat(`"`, 16385))); status != http.StatusRequestEntityTooLarge {
		t.Fatalf("32770 encoded bytes: status %d, want 413", status)
	}
	if status := post(sendBatch("s-00000000000000aa", strings.Repeat(`"`, 16384))); status != http.StatusAccepted {
		t.Fatalf("32768 encoded bytes: status %d, want 202", status)
	}
}

func TestSendRefusesTrailingDataAndBlankComments(t *testing.T) {
	_, post := serve(t)
	for name, body := range map[string]string{
		"trailing data":    sendBatch("s-00000000000000aa", "ok") + " GARBAGE{",
		"trailing bracket": sendBatch("s-00000000000000aa", "ok") + "]} not json",
		"trailing object":  sendBatch("s-00000000000000aa", "ok") + "{}",
		"blank comment":    sendBatch("s-00000000000000aa", "   "),
	} {
		if status := post(body); status != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, status)
		}
	}
}

func TestSendKeepsTheResultLineInsideTheTail(t *testing.T) {
	_, post := serve(t)
	comments := strings.TrimSuffix(strings.Repeat(`{"text":"a"},`, 2000), ",")
	body := `{"round":"r1","token":"tok-1","submission":"s-00000000000000aa","comments":[` + comments + `]}`
	if status := post(body); status != http.StatusRequestEntityTooLarge {
		t.Fatalf("2000 one-byte comments: status %d, want 413", status)
	}
}
