package live

import (
	"testing"
	"time"

	"github.com/taekwondodev/lavagna/internal/cdptest"
)

// Widths were tried only in headless Chrome, never on a real phone.
const (
	desktopWidth = 1280
	narrowWidth  = 760
	phoneWidth   = 390
	pageHeight   = 760
)

const wideRound = "::: phase Spese\n:::\n" + `# Dove si aggiunge una spesa? {id="spesa"}
## Capire
Lo schermo di oggi.

<div class="desk" id="desk">desktop</div>

<div class="mobile" id="mobile">telefono</div>

<p id="narrow">stretto</p>

## Decidere
- [inline] Riga in linea {recommended}
- [button] Pulsante in alto

# Quanto teniamo le spese? {id="retention"}
## Capire
Solo testo.

## Decidere
- [week] Una settimana
- [month] Un mese
`

func wideFiles() map[string]string {
	return map[string]string{"spesa/screens.css": `.desk { width: 1200px; height: 1400px; } .mobile { width: 360px; height: 300px; }`}
}

// box returns a shell element's rectangle, or nil when it is not rendered.
func box(t *testing.T, p *cdptest.Page, selector string) *rect {
	t.Helper()
	var r *rect
	p.MustEval(`(() => { const el = document.querySelector(`+jsString(selector)+`);
		if (!el || !el.getClientRects().length) return null;
		const r = el.getBoundingClientRect(); return {Left: r.left, Top: r.top, Right: r.right, Bottom: r.bottom}; })()`, &r)
	return r
}

type rect struct{ Left, Top, Right, Bottom float64 }

// wantHeads waits until the frame's raw blocks, listed as their header label
// and control, settle on want: fitting follows the column's final width.
func wantHeads(t *testing.T, f *cdptest.Frame, want, why string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := heads(f)
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("blocks %q, want %q: %s", got, want, why)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// settled waits until the frame's height stops changing: fitting a wide block
// shrinks it and moves 03 under a click.
func settled(p *cdptest.Page) {
	p.MustEval(`new Promise(done => {
		let last = '';
		const tick = () => {
			const height = document.querySelector('#content').style.height;
			if (height && height === last) done(true);
			else { last = height; setTimeout(tick, 150); }
		};
		tick();
	})`, nil)
}

func heads(f *cdptest.Frame) string {
	return frameString(f, `[...document.querySelectorAll('.raw')].map(b => {
		const head = b.querySelector('.raw-head');
		if (!b.classList.contains('wide') || !head) return '-';
		const control = head.querySelector('.raw-expand');
		return head.querySelector('.raw-scale').textContent + (control.hidden ? '' : ' [' + control.textContent + ']');
	}).join(' | ')`)
}

func TestDecisionTablesKeepReadableColumnsWithLongReasons(t *testing.T) {
	run := newPhaseRun(t)
	run.call(`# Is this interaction ready for your grilling sessions? {id="acceptance"}
## Decidere
- [accept] The interaction is ready
- [revise] Fix the concerns I describe first
`, nil)
	p := cdptest.Start(t).Open(run.url, desktopWidth, pageHeight)
	p.WaitFor(`document.querySelector('.option')`)
	p.Click(`.option[data-option="accept"]`)
	p.Click("#send")
	run.outcome()
	run.call(`::: settled acceptance
You accepted the actual per-question interaction without messages. The integration build passes its full Go suite with no skipped tests and all three token thresholds. This accepts the interaction for your grilling sessions; it does not bypass the skills workspace boundary or permit either repository to land alone. The alternative was to address user-reported interaction changes first; none were submitted.
:::
# Confirm the acceptance outcome {id="confirmation"}
## Capire
::: recap
:::
## Decidere
- [confirm] Confirm
- [correct] Correct
`, nil)
	p.WaitFor(`document.querySelector('#round-label').textContent === 'r2'`)
	p.Click(`.card[data-target="confirmation"]`)
	p.WaitFor(`document.querySelector('#content')`)
	f := p.Frame("#content")
	f.WaitFor(`document.querySelector('.recap table')`)
	const readable = `(() => {
		const table = document.querySelector('table');
		return [...table.querySelectorAll('thead th')].every(th => {
			const range = document.createRange(); range.selectNodeContents(th);
			return range.getClientRects().length === 1;
		}) && document.documentElement.scrollWidth <= innerWidth;
	})()`
	for _, width := range []int{desktopWidth, phoneWidth} {
		p.Resize(width, pageHeight, width == phoneWidth)
		f.WaitFor(`innerWidth <= ` + jsonOf(width))
		var ok bool
		f.MustEval(readable, &ok)
		if !ok {
			t.Errorf("recap at %d px splits headers or overflows the frame", width)
		}
	}
	p.Click(`.card[data-target=":overview"]`)
	for _, width := range []int{desktopWidth, phoneWidth} {
		p.Resize(width, pageHeight, width == phoneWidth)
		var ok bool
		p.MustEval(readable, &ok)
		if !ok {
			t.Errorf("Overview at %d px splits headers or overflows the page", width)
		}
	}
}

func TestNarrowWindowKeepsTheDiscussionAndPhoneOpensItAsASheet(t *testing.T) {
	run := newPhaseRun(t)
	run.call(wideRound, wideFiles())
	p := cdptest.Start(t).Open(run.url, narrowWidth, pageHeight)
	p.WaitFor(`document.querySelector('#content')`)

	rail, center, discussion := box(t, p, "#rail"), box(t, p, "#center"), box(t, p, "#discussion")
	if rail.Right-rail.Left > 90 || discussion == nil || discussion.Left < center.Right-1 {
		t.Errorf("at %d px: rail %v, center %v, discussion %v; want a number rail and the discussion on the right", narrowWidth, rail, center, discussion)
	}
	if box(t, p, `.card[data-target="spesa"] .card-title`) != nil || box(t, p, ".legend") != nil {
		t.Error("the narrow rail still shows titles or the legend")
	}
	wantText(t, p, `.card[data-target="spesa"] .card-n`, "Q1")
	if box(t, p, "#sheet-toggle") != nil {
		t.Error("the discussion toggle shows outside phone width")
	}

	p.Resize(phoneWidth, pageHeight, true)
	p.WaitFor(`!document.querySelector('#discussion').getClientRects().length`)
	rail, center = box(t, p, "#rail"), box(t, p, "#center")
	if row := evalString(p, `String([...document.querySelectorAll('#rail .card')].every((c, i, all) => {
		const r = c.getBoundingClientRect(), first = all[0].getBoundingClientRect();
		return r.top < first.bottom && (i === 0 || r.left > all[i - 1].getBoundingClientRect().right);
	}))`); row != "true" {
		t.Error("phone rail cards are not one row")
	}
	if rail.Top != 54 || rail.Bottom > center.Top || rail.Bottom-rail.Top > 80 {
		t.Errorf("phone rail %v, center %v: want a strip under the title bar", rail, center)
	}
	wantText(t, p, "#sheet-toggle", "Discussione")
	p.Type("#free-text", "x")
	p.Click("#sheet-toggle")
	sheet, footer := box(t, p, "#discussion"), box(t, p, "#footer")
	if sheet == nil || sheet.Left != 0 || sheet.Right != phoneWidth || sheet.Bottom != footer.Top || sheet.Top <= rail.Bottom {
		t.Errorf("open sheet %v, footer %v, rail %v: want a sheet above the footer, below the strip", sheet, footer, rail)
	}
	wantText(t, p, "#sheet-toggle", "Chiudi discussione")
	p.Type("#composer", "Serve anche da telefono?")
	p.Click("#stage-message")
	wantText(t, p, "#thread .message-staged", "Tu · in bozza×Serve anche da telefono?")
	p.Click("#sheet-toggle")
	if box(t, p, "#discussion") != nil {
		t.Error("closing the sheet left the discussion visible")
	}
	wantText(t, p, "#sheet-toggle", "Discussione 1")
	wantText(t, p, "#summary", "2 in bozza · Q1 → ✎ · Q1 +1 msg")
}

func TestWideBlocksFitAndExpand(t *testing.T) {
	run := newPhaseRun(t)
	run.call(wideRound, wideFiles())
	p := cdptest.Start(t).Open(run.url, desktopWidth, pageHeight)
	p.WaitFor(`document.querySelector('#content')`)
	f := p.Frame("#content")
	f.WaitFor(`document.querySelector('.raw.wide')`)

	wantHeads(t, f, "Schermata 1200 px · 49 % [Espandi a tutta pagina] | - | -", "the wide screen is fitted with Expand, the others untouched")
	if got := frameString(f, `String(Math.round(document.querySelector('#desk').getBoundingClientRect().right) <= innerWidth)`); got != "true" {
		t.Error("the fitted screen overflows the column")
	}

	f.Click(".raw-expand")
	p.WaitFor(`document.querySelector('#page').dataset.expanded === 'fit'`)
	if box(t, p, "#rail") != nil || box(t, p, "#discussion") != nil || box(t, p, "#footer") == nil {
		t.Error("desktop Expand must hide only rail and discussion")
	}
	f.WaitFor(`document.querySelector('.raw-expand').textContent === 'Riduci'`)
	wantHeads(t, f, "Schermata 1200 px · 97 % [Riduci] | - | -", "the expanded screen refits to the wider column")
	p.Press("Escape", 27, 0)
	p.WaitFor(`!document.querySelector('#page').dataset.expanded`)
	f.WaitFor(`document.querySelector('.raw-expand').textContent === 'Espandi a tutta pagina'`)
	if box(t, p, "#rail") == nil || box(t, p, "#discussion") == nil {
		t.Error("Esc in the frame did not restore rail and discussion")
	}

	f.Click(".raw-expand")
	p.WaitFor(`document.querySelector('#page').dataset.expanded === 'fit'`)
	p.MustEval(`document.querySelector('#send').focus()`, nil)
	p.Press("Escape", 27, 0)
	p.WaitFor(`!document.querySelector('#page').dataset.expanded`)
	f.WaitFor(`document.querySelector('.raw-expand').textContent === 'Espandi a tutta pagina'`)

	f.Click(".raw-expand")
	p.WaitFor(`document.querySelector('#page').dataset.expanded === 'fit'`)
	f.Click(".raw-expand")
	p.WaitFor(`!document.querySelector('#page').dataset.expanded`)

	p.Resize(phoneWidth, pageHeight, true)
	wantHeads(t, f, "Schermata 1200 px · 27 % [Espandi a tutta pagina] | Schermata 360 px · 88 % | -", "Expand only below 85 %")
	f.Click(".raw-expand")
	p.WaitFor(`document.querySelector('#page').dataset.expanded === 'native'`)
	for _, hidden := range []string{".titlebar", "#rail", "#footer"} {
		if box(t, p, hidden) != nil {
			t.Errorf("phone Expand left %s visible", hidden)
		}
	}
	if bar := box(t, p, "#expand-bar"); bar == nil || *bar != (rect{0, 0, phoneWidth, 44}) {
		t.Errorf("phone Expand bar %v, want the shell's bar at the top", bar)
	}
	wantText(t, p, "#expand-label", "Q1 · schermata a grandezza reale")
	if frame := box(t, p, "#content"); *frame != (rect{0, 44, phoneWidth, pageHeight}) {
		t.Errorf("phone Expand frame %v, want the rest of the screen", frame)
	}
	f.WaitFor(`document.documentElement.dataset.expanded === 'native'`)
	var native struct {
		Head      bool
		Zoom      string
		Left, Top float64
		Covers    bool
	}
	f.MustEval(`(() => {
		const block = document.querySelector('.raw.expanded');
		const stage = block.querySelector('.raw-stage');
		stage.scrollTo(300, 500);
		const r = block.getBoundingClientRect();
		return {Head: block.querySelector('.raw-head').getClientRects().length > 0,
			Zoom: getComputedStyle(stage).zoom, Left: stage.scrollLeft, Top: stage.scrollTop,
			Covers: r.left === 0 && r.top === 0 && r.right === innerWidth && r.bottom === innerHeight};
	})()`, &native)
	if native.Head || native.Zoom != "1" || native.Left != 300 || native.Top != 500 || !native.Covers {
		t.Errorf("phone Expand %+v: want the screen at native size under the bar, scrolling both ways", native)
	}
	p.Click("#reduce")
	p.WaitFor(`!document.querySelector('#page').dataset.expanded`)
	f.WaitFor(`!document.documentElement.dataset.expanded && !document.querySelector('.raw.expanded')`)
	if box(t, p, ".titlebar") == nil || box(t, p, "#footer") == nil || box(t, p, "#rail") == nil {
		t.Error("Riduci did not restore the phone chrome")
	}

	p.Resize(desktopWidth, pageHeight, false)
	wantHeads(t, f, "Schermata 1200 px · 49 % [Espandi a tutta pagina] | - | -", "back at desktop width")
	f.Click(".raw-expand")
	p.WaitFor(`document.querySelector('#page').dataset.expanded === 'fit'`)
	p.MustEval(`show('retention')`, nil)
	p.WaitFor(`!document.querySelector('#page').dataset.expanded && document.querySelector('#content').src.includes('/retention/')`)
	if box(t, p, "#rail") == nil || box(t, p, "#discussion") == nil {
		t.Error("a question switch did not leave expansion")
	}
}

// forgeScript asks for expansion on any click in the frame, with fields the
// shell must ignore.
const forgeScript = `document.addEventListener('click', () => parent.postMessage({lavagna: 'expand', active: true, option: 'inline', send: true}, '*'));`

func TestForgedExpandChangesOnlyPresentation(t *testing.T) {
	run := newPhaseRun(t)
	files := wideFiles()
	files["spesa/forge.js"] = forgeScript
	files["retention/forge.js"] = forgeScript
	run.call(wideRound, files)
	p := cdptest.Start(t).Open(run.url, desktopWidth, pageHeight)
	p.WaitFor(`document.querySelector('#content')`)
	f := p.Frame("#content")
	f.WaitFor(`document.querySelector('.raw.wide')`)
	settled(p)
	p.Click(`.option[data-option="button"]`)
	p.Type("#composer", "Bozza")
	wantText(t, p, "#summary", "2 in bozza · Q1 → B · Q1 +1 msg")
	draft := func() string {
		return evalString(p, `[localStorage.getItem('lavagna:' + location.pathname), document.querySelector('#summary').textContent,
			document.querySelector('#send').disabled, document.querySelector('#composer').value].join('\n')`)
	}
	before := draft()

	f.MustEval(`parent.postMessage({lavagna: 'expand', active: true}, '*')`, nil)
	time.Sleep(200 * time.Millisecond)
	if got := evalString(p, `document.querySelector('#page').dataset.expanded || 'none'`); got != "none" {
		t.Errorf("an expand without a gesture in the frame changed the page: %s", got)
	}
	// Frame.Click scrolls the window, not the center column Type scrolled.
	p.MustEval(`document.querySelector('#center').scrollTop = 0`, nil)
	f.Click(".chapter-body > p")
	p.WaitFor(`document.querySelector('#page').dataset.expanded === 'fit'`)
	f.WaitFor(`document.querySelector('.raw.expanded #desk')`)
	if got := draft(); got != before {
		t.Errorf("a forged expand changed the draft:\n%s\nwas\n%s", got, before)
	}
	f.MustEval(`parent.postMessage({lavagna: 'expand', active: false}, '*')`, nil)
	p.WaitFor(`!document.querySelector('#page').dataset.expanded`)

	p.Click(`.card[data-target="retention"]`)
	p.WaitFor(`document.querySelector('#content') && document.querySelector('#content').src.includes('/retention/')`)
	p.MustEval(`window.seen = []; new MutationObserver(() => seen.push(document.querySelector('#page').dataset.expanded || '')).observe(document.querySelector('#page'), {attributes: true})`, nil)
	f = p.Frame("#content")
	f.Click(".chapter-body > p")
	p.WaitFor(`seen.join() === 'fit,'`)
	if got := evalString(p, `document.querySelector('#page').dataset.expanded || 'none'`); got != "none" {
		t.Errorf("a forged expand without a wide block left the page %s", got)
	}
	p.Click(`.card[data-target="spesa"]`)
	wantText(t, p, "#summary", "2 in bozza · Q1 → B · Q1 +1 msg")
}

func TestRoundScriptCannotTrapThePhoneExpansion(t *testing.T) {
	run := newPhaseRun(t)
	run.call(wideRound, wideFiles())
	p := cdptest.Start(t).Open(run.url, phoneWidth, pageHeight)
	p.Resize(phoneWidth, pageHeight, true)
	p.WaitFor(`document.querySelector('#content')`)
	f := p.Frame("#content")
	wantHeads(t, f, "Schermata 1200 px · 27 % [Espandi a tutta pagina] | Schermata 360 px · 88 % | -", "the phone fit")
	f.Click(".raw-expand")
	p.WaitFor(`document.querySelector('#page').dataset.expanded === 'native'`)

	// The round script removes the frame's Riduci, swallows Escape and keeps
	// asking for expansion.
	f.MustEval(`(() => {
		for (const head of document.querySelectorAll('.raw-head')) head.remove();
		addEventListener('keydown', event => { if (event.key === 'Escape') event.stopImmediatePropagation(); }, true);
		setInterval(() => parent.postMessage({lavagna: 'expand', active: true}, '*'), 20);
	})()`, nil)
	p.Press("Escape", 27, 0)
	time.Sleep(200 * time.Millisecond)
	if evalString(p, `document.querySelector('#page').dataset.expanded || ''`) != "native" {
		t.Fatal("the swallowed Escape left expansion; the trap is not exercised")
	}
	p.Click("#reduce")
	p.WaitFor(`!document.querySelector('#page').dataset.expanded`)
	time.Sleep(300 * time.Millisecond)
	if got := evalString(p, `document.querySelector('#page').dataset.expanded || 'none'`); got != "none" {
		t.Errorf("the round script expanded the page again without a gesture: %s", got)
	}
	if box(t, p, ".titlebar") == nil || box(t, p, "#footer") == nil || box(t, p, "#rail") == nil {
		t.Error("the shell's Riduci did not restore the phone chrome")
	}
}
