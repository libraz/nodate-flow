package email

import (
	"strings"
	"testing"
)

func TestRenderHTMLHeader(t *testing.T) {
	tests := []struct {
		name  string
		brand Brand
		want  []string
		not   []string
	}{
		{"name only", Brand{Name: "Acme"}, []string{"<strong>Acme</strong>"}, []string{"<img"}},
		{"https logo", Brand{Name: "Acme", LogoURL: "https://c.example/l.svg", IconURL: "https://c.example/i.svg"},
			[]string{`<img src="https://c.example/l.svg" alt="Acme"`, "height:40px", "max-width:240px"},
			[]string{"i.svg", "<strong>"}},
		{"https icon", Brand{Name: "Acme", IconURL: "https://c.example/i.svg"},
			[]string{`<img src="https://c.example/i.svg" alt=""`, `width="32"`, "<strong>Acme</strong>"}, nil},
		{"uppercase scheme", Brand{Name: "Acme", LogoURL: "HTTPS://c.example/l.svg"},
			[]string{`src="HTTPS://c.example/l.svg"`}, nil},
		{"data logo ignored", Brand{Name: "Acme", LogoURL: "data:image/png;base64,AAAA"},
			[]string{"<strong>Acme</strong>"}, []string{"<img"}},
		{"data logo with https icon", Brand{Name: "Acme", LogoURL: "DATA:image/png;base64,AAAA", IconURL: "https://c.example/i.svg"},
			[]string{`src="https://c.example/i.svg"`, "<strong>Acme</strong>"}, []string{"base64"}},
		{"escaping", Brand{Name: `A<b>&"c"`, LogoURL: `https://c.example/l.svg?a=1&b="2"`},
			[]string{`alt="A&lt;b&gt;&amp;&#34;c&#34;"`, `a=1&amp;b=&#34;2&#34;`}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderHTML(tt.brand, "x")
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("missing %q in:\n%s", w, got)
				}
			}
			for _, n := range tt.not {
				if strings.Contains(got, n) {
					t.Errorf("unexpected %q in:\n%s", n, got)
				}
			}
		})
	}
}

func TestRenderHTMLBody(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"escape", `a <b> & "c"`, `<p>a &lt;b&gt; &amp; &#34;c&#34;</p>`},
		{"paragraphs", "one\n\ntwo", "<p>one</p>\n<p>two</p>"},
		{"line break", "one\ntwo", "<p>one<br>two</p>"},
		{"crlf", "one\r\ntwo\r\n\r\nthree", "<p>one<br>two</p>\n<p>three</p>"},
		{"link", "go https://x.example/a?b=1&c=2 now",
			`<p>go <a href="https://x.example/a?b=1&amp;c=2">https://x.example/a?b=1&amp;c=2</a> now</p>`},
		{"trailing punctuation", "see https://x.example/p.", `<a href="https://x.example/p">https://x.example/p</a>.</p>`},
		{"trailing paren", "(https://x.example/p)", `(<a href="https://x.example/p">https://x.example/p</a>)</p>`},
		{"trailing run", "https://x.example/p?!.,;:)", `<a href="https://x.example/p">https://x.example/p</a>?!.,;:)</p>`},
		{"angle delimiter", "<https://x.example/p>", `&lt;<a href="https://x.example/p">https://x.example/p</a>&gt;`},
		{"quote delimiter", `"https://x.example/p"`, `&#34;<a href="https://x.example/p">https://x.example/p</a>&#34;`},
		{"http", "http://x.example/p", `<a href="http://x.example/p">`},
		{"bare scheme", "https:// x", "<p>https:// x</p>"},
		{"link at line end", "https://x.example/p\nnext", `</a><br>next`},
		{"empty", "", "<body style"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderHTML(Brand{Name: "Acme"}, tt.body)
			if !strings.Contains(got, tt.want) {
				t.Errorf("missing %q in:\n%s", tt.want, got)
			}
		})
	}
}
