package round

import (
	"html"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

// lexicon describes the lexical shape of a language closely enough to colour
// comments, strings, numbers, keywords and calls in one pass over an excerpt.
type lexicon struct {
	comments []string    // line comment prefixes
	blocks   [][2]string // block comments, open and close
	quotes   string      // quotes of single-line strings with backslash escapes
	raw      [][2]string // strings that may span lines, without escapes
	keywords map[string]bool
	literals map[string]bool
}

func words(s string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(s) {
		m[w] = true
	}
	return m
}

var (
	cBlock   = [][2]string{{"/*", "*/"}}
	cLiteral = words("true false null")
	jsKeys   = "async await break case catch class const continue debugger default delete do else export extends finally for from function get if import in instanceof let new of return set static super switch this throw try typeof var void while with yield"
	jsLex    = &lexicon{[]string{"//"}, cBlock, `"'`, [][2]string{{"`", "`"}}, words(jsKeys), words("true false null undefined NaN Infinity")}
	tsLex    = &lexicon{[]string{"//"}, cBlock, `"'`, [][2]string{{"`", "`"}}, words(jsKeys + " abstract any as asserts boolean declare enum implements infer interface is keyof namespace never number private protected public readonly satisfies string symbol type unknown"), words("true false null undefined NaN Infinity")}
	cLex     = &lexicon{[]string{"//"}, cBlock, `"'`, nil, words("auto break case char const continue default do double else enum extern float for goto if inline int long register restrict return short signed sizeof static struct switch typedef union unsigned void volatile while class namespace template typename public private protected virtual override new delete this using try catch throw nullptr constexpr bool"), words("true false NULL nullptr")}
	sqlKeys  = "select from where and or not insert into values update set delete create table index view drop alter add primary key foreign references join left right inner outer on group by order having limit offset as distinct union all case when then else end begin commit rollback transaction exists in is like returning integer text varchar boolean"
	shLex    = &lexicon{[]string{"#"}, nil, `"'`, nil, words("if then else elif fi case esac for while until do done in function return local export readonly set unset shift exit break continue source alias and or not end begin switch"), words("true false")}
)

// lexicons maps a lowercase file extension, or a base name without one, to its
// language. Excerpts of other files are shown uncoloured.
var lexicons = map[string]*lexicon{
	".go": {[]string{"//"}, cBlock, `"'`, [][2]string{{"`", "`"}}, words("break case chan const continue default defer else fallthrough for func go goto if import interface map package range return select struct switch type var any bool byte complex64 complex128 error float32 float64 int int8 int16 int32 int64 rune string uint uint8 uint16 uint32 uint64 uintptr"), words("true false nil iota")},
	".js": jsLex, ".mjs": jsLex, ".cjs": jsLex, ".jsx": jsLex,
	".ts": tsLex, ".mts": tsLex, ".cts": tsLex, ".tsx": tsLex,
	".py":    {[]string{"#"}, nil, `"'`, [][2]string{{`"""`, `"""`}, {"'''", "'''"}}, words("and as assert async await break class continue def del elif else except finally for from global if import in is lambda match case nonlocal not or pass raise return try while with yield self"), words("True False None")},
	".rs":    {[]string{"//"}, cBlock, `"`, nil, words("as async await break const continue crate dyn else enum extern fn for if impl in let loop match mod move mut pub ref return self Self static struct super trait type unsafe use where while bool char str u8 u16 u32 u64 u128 usize i8 i16 i32 i64 i128 isize f32 f64 String Vec Option Result Box"), words("true false None Some Ok Err")},
	".java":  {[]string{"//"}, cBlock, `"'`, [][2]string{{`"""`, `"""`}}, words("abstract assert boolean break byte case catch char class const continue default do double else enum extends final finally float for if implements import instanceof int interface long native new package private protected public record return short static super switch synchronized this throw throws try var void volatile while"), cLiteral},
	".kt":    {[]string{"//"}, cBlock, `"'`, [][2]string{{`"""`, `"""`}}, words("as break class continue data do else enum for fun if import in interface is object override package private protected public return sealed super this throw try typealias val var when while suspend"), cLiteral},
	".swift": {[]string{"//"}, cBlock, `"`, [][2]string{{`"""`, `"""`}}, words("actor as async await break case catch class continue default defer do else enum extension fileprivate for func guard if import in init inout internal is let private protocol public return self Self static struct switch throw throws try var where while"), words("true false nil")},
	".c":     cLex, ".h": cLex, ".cc": cLex, ".cpp": cLex, ".cxx": cLex, ".hpp": cLex,
	".cs":  {[]string{"//"}, cBlock, `"'`, nil, words("abstract as async await base bool break byte case catch char class const continue decimal default delegate do double else enum event explicit extern finally fixed float for foreach if implicit in int interface internal is lock long namespace new object operator out override params private protected public readonly record ref return sealed short static string struct switch this throw try typeof uint ulong using var virtual void volatile while"), words("true false null")},
	".rb":  {[]string{"#"}, nil, `"'`, nil, words("alias and begin break case class def do else elsif end ensure for if in module next not or redo rescue retry return self super then unless until when while yield require attr_reader attr_accessor"), words("true false nil")},
	".php": {[]string{"//", "#"}, cBlock, `"'`, nil, words("abstract and array as break case catch class const continue default do echo else elseif extends final finally fn for foreach function global if implements interface instanceof namespace new or private protected public readonly return static switch throw trait try use var while"), words("true false null")},
	".sh":  shLex, ".bash": shLex, ".zsh": shLex, ".fish": shLex, "makefile": shLex, "dockerfile": shLex,
	".sql":  {[]string{"--"}, cBlock, `'"`, nil, words(sqlKeys + " " + strings.ToUpper(sqlKeys)), words("null true false NULL TRUE FALSE")},
	".css":  {nil, cBlock, `"'`, nil, nil, words("important inherit initial unset none auto")},
	".json": {nil, nil, `"`, nil, nil, cLiteral},
	".yaml": {[]string{"#"}, nil, `"'`, nil, nil, words("true false null yes no on off")},
	".yml":  {[]string{"#"}, nil, `"'`, nil, nil, words("true false null yes no on off")},
	".toml": {[]string{"#"}, nil, `"'`, [][2]string{{`"""`, `"""`}, {"'''", "'''"}}, nil, words("true false")},
}

func lexiconFor(file string) *lexicon {
	base := strings.ToLower(path.Base(file))
	if lx, ok := lexicons[path.Ext(base)]; ok {
		return lx
	}
	return lexicons[base]
}

// highlight returns the escaped HTML of each line of text. Token spans close at
// every line end and reopen on the next line, so each line stays a complete
// element. Without a lexicon the lines are only escaped.
func highlight(file, text string) []string {
	lx := lexiconFor(file)
	if lx == nil {
		lines := strings.Split(text, "\n")
		for i, l := range lines {
			lines[i] = html.EscapeString(l)
		}
		return lines
	}
	out := make([]string, 0, strings.Count(text, "\n")+1)
	var b strings.Builder
	emit := func(class, tok string) {
		for {
			part, rest, more := strings.Cut(tok, "\n")
			switch {
			case part == "":
			case class == "":
				b.WriteString(html.EscapeString(part))
			default:
				b.WriteString(`<span class="hl-`)
				b.WriteString(class)
				b.WriteString(`">`)
				b.WriteString(html.EscapeString(part))
				b.WriteString(`</span>`)
			}
			if !more {
				return
			}
			out = append(out, b.String())
			b.Reset()
			tok = rest
		}
	}
	plain := 0
	flush := func(i int) {
		if plain < i {
			emit("", text[plain:i])
		}
	}
	for i := 0; i < len(text); {
		if class, end := lx.token(text, i); end > i {
			flush(i)
			emit(class, text[i:end])
			i, plain = end, end
			continue
		}
		r, size := utf8.DecodeRuneInString(text[i:])
		if !isWord(r) {
			i += size
			continue
		}
		end := i
		for end < len(text) {
			r, size := utf8.DecodeRuneInString(text[end:])
			if !isWord(r) {
				break
			}
			end += size
		}
		word, class := text[i:end], ""
		switch {
		case lx.keywords[word]:
			class = "k"
		case lx.literals[word]:
			class = "l"
		case strings.HasPrefix(text[end:], "("):
			class = "f"
		}
		if class != "" {
			flush(i)
			emit(class, word)
			plain = end
		}
		i = end
	}
	flush(len(text))
	return append(out, b.String())
}

// token recognises a comment, string or number starting at i and returns its
// class and end, or end == i when none starts there.
func (lx *lexicon) token(text string, i int) (string, int) {
	rest := text[i:]
	for _, p := range lx.comments {
		if strings.HasPrefix(rest, p) {
			return "c", i + lineEnd(rest)
		}
	}
	for _, d := range lx.blocks {
		if strings.HasPrefix(rest, d[0]) {
			return "c", i + closing(rest, d)
		}
	}
	for _, d := range lx.raw {
		if strings.HasPrefix(rest, d[0]) {
			return "s", i + closing(rest, d)
		}
	}
	if q := rest[0]; strings.IndexByte(lx.quotes, q) >= 0 {
		end := 1
		for end < len(rest) && rest[end] != q && rest[end] != '\n' {
			if rest[end] == '\\' && end+1 < len(rest) && rest[end+1] != '\n' {
				end++
			}
			end++
		}
		if end < len(rest) && rest[end] == q {
			end++
		}
		return "s", i + end
	}
	if rest[0] >= '0' && rest[0] <= '9' {
		end := 1
		for end < len(rest) {
			if rest[end] == '.' && end+1 < len(rest) && rest[end+1] >= '0' && rest[end+1] <= '9' {
				end++
				continue
			}
			r, size := utf8.DecodeRuneInString(rest[end:])
			if !isWord(r) {
				break
			}
			end += size
		}
		return "n", i + end
	}
	return "", i
}

func isWord(r rune) bool {
	return r == '_' || r == '$' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func lineEnd(s string) int {
	if n := strings.IndexByte(s, '\n'); n >= 0 {
		return n
	}
	return len(s)
}

// closing returns the end of a delimited token, or the end of s when it does
// not close inside the excerpt.
func closing(s string, d [2]string) int {
	if n := strings.Index(s[len(d[0]):], d[1]); n >= 0 {
		return len(d[0]) + n + len(d[1])
	}
	return len(s)
}
