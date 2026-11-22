package richtext

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type mapResolver map[string]string

func (m mapResolver) UserName(gid string) (string, bool) {
	v, ok := m[gid]
	return v, ok
}

var testUsers = mapResolver{"123": "Hanako"}

// goldenCases are derived by hand from the rule table; each name states the
// rule it pins.
var goldenCases = []struct{ name, in, want string }{
	{"strong_b", `<body><strong>a</strong> <b>b</b></body>`, `**a** **b**`},
	{"em_i", `<body><em>a</em> <i>b</i></body>`, `*a* *b*`},
	{"underline_dropped", `<body>a <u>x</u> b</body>`, `a x b`},
	{"strike", `<body><s>a</s> <strike>b</strike></body>`, `~~a~~ ~~b~~`},
	{"code", `<body><code>x *y*</code></body>`, "`x *y*`"},
	{"code_with_backtick", "<body><code>a`b</code></body>", "`` a`b ``"},
	{"pre_verbatim", "<body><pre>a *b*\n  c</pre></body>", "```\na *b*\n  c\n```"},
	{"pre_with_fence", "<body><pre>x ``` y</pre></body>", "````\nx ``` y\n````"},
	{"mention_unknown", `<body><a data-asana-gid="999" data-asana-type="user"></a></body>`, `@unknown user`},
	{"mention_resolved", `<body>cc <a data-asana-gid="123" data-asana-type="user">ignored</a></body>`, `cc @Hanako`},
	{"link", `<body><a href="http://e.x/p?q=1">site</a></body>`, `[site](http://e.x/p?q=1)`},
	{"link_empty_text", `<body><a href="http://e.x"></a></body>`, `[http://e.x](http://e.x)`},
	{"text_escapes_bracket", `<body><a href="h">a]b</a></body>`, `[a\]b](h)`},
	{"anchor_without_href", `<body><a>t</a></body>`, `t`},
	{"blockquote", "<body><blockquote>a\nb</blockquote></body>", "> a\n> b"},
	{"blockquote_nested", `<body><blockquote>a<blockquote>b</blockquote></blockquote></body>`, "> a\n>\n> > b"},
	{"ul", `<body><ul><li>a</li><li>b</li></ul></body>`, "- a\n- b"},
	{"ol", `<body><ol><li>a</li><li>b</li></ol></body>`, "1. a\n2. b"},
	{
		"ol_marker_width_ten",
		`<body><ol><li>a</li><li>b</li><li>c</li><li>d</li><li>e</li><li>f</li><li>g</li><li>h</li><li>i</li><li>j<ul><li>n</li></ul></li></ol></body>`,
		"1. a\n2. b\n3. c\n4. d\n5. e\n6. f\n7. g\n8. h\n9. i\n10. j\n    - n",
	},
	{"list_in_quote", `<body><blockquote><ul><li>a</li></ul></blockquote></body>`, "> - a"},
	{"quote_in_list", `<body><ul><li>a<blockquote>q</blockquote></li></ul></body>`, "- a\n\n  > q"},
	{"list_unclosed_li", `<body><ul><li>a<li>b</ul></body>`, "- a\n- b"},
	{"list_stray_text", `<body><ul>x<li>a</li></ul></body>`, "- x\n- a"},
	{"list_empty_item", `<body><ul><li></li><li>a</li></ul></body>`, "-\n- a"},
	{"headings", `<body><h1>T</h1><h2>S</h2></body>`, "# T\n\n## S"},
	{"heading_newline_to_space", "<body><h1>a\nb</h1></body>", "# a b"},
	{"hr", `<body>a<hr>b</body>`, "a\n\n---\n\nb"},
	{"img_with_gid_dropped", `<body>x<img data-asana-gid="1" src="s">y</body>`, `xy`},
	{"img", `<body><img src="http://e/i.png" alt="pic"></body>`, `![pic](http://e/i.png)`},
	{
		"table_ragged_and_pipe",
		`<body><table><tr><td>a</td><td>b|c</td></tr><tr><td>d</td></tr></table></body>`,
		"| a | b\\|c |\n| --- | --- |\n| d |  |",
	},
	{"table_cell_newline", "<body><table><tr><td>a\nb</td></tr></table></body>", "| a b |\n| --- |"},
	{"table_cell_no_line_start_escape", `<body><table><tr><td>- x</td></tr></table></body>`, "| - x |\n| --- |"},
	{"table_cell_code_pipe", `<body><table><tr><td><code>a|b</code></td></tr></table></body>`, "| `a\\|b` |\n| --- |"},
	{"table_without_rows", `<body>a<table></table>b</body>`, "a\n\nb"},
	{"unknown_tag", `<body>a<div>b<span>c</span></div>d</body>`, `abcd`},
	{"paragraph_and_br_are_unknown", `<body><p>a</p><p>b<br>c</p></body>`, `abc`},
	{"comment_dropped", `<body>a<!-- hidden -->b</body>`, `ab`},
	{"entities", `<body>a &amp; b &lt;i&gt; &#65;&copy;</body>`, `a & b \<i\> A©`},
	{"escape_always", "<body>\\ * _ ` ~ [ ] < ></body>", "\\\\ \\* \\_ \\` \\~ \\[ \\] \\< \\>"},
	{
		"escape_line_start",
		"<body>#a\n-b\n+c\n=d\n1) e\n12. f\n1.5</body>",
		"\\#a\n\\-b\n\\+c\n\\=d\n1\\) e\n12\\. f\n1\\.5",
	},
	{"escape_line_start_not_midline", `<body>a - b # c + d = e 1. f</body>`, `a - b # c + d = e 1. f`},
	{"escape_pipe_only_in_cells", `<body>a | b</body>`, `a | b`},
	{"escape_inside_list_item", `<body><ul><li>- x</li><li>1. y</li></ul></body>`, "- \\- x\n- 1\\. y"},
	{"whitespace_blank_runs", "<body>a  \nb\n\n\n\nc   </body>", "a\nb\n\nc"},
	{"whitespace_trim", "<body>\n  x \n</body>", `x`},
	{"whitespace_outside_emphasis", `<body>x<strong> a </strong>y</body>`, `x **a** y`},
	{"empty_emphasis", `<body>a<strong></strong>b</body>`, `ab`},
	{"blocks_one_blank_line", `<body>a<ul><li>b</li></ul>c<hr></body>`, "a\n\n- b\n\nc\n\n---"},
	{"malformed_stray_end_tags", `<body>a</strong></p>b</body>`, `ab`},
	{"malformed_unclosed_nested", `<body><em><strong>x`, `***x***`},
	{"malformed_overlap", `<body><strong>a<em>b</strong>c</em></body>`, `**a*b***c`},
	{"malformed_lone_angle", `<body>a < b</body>`, `a \< b`},
	{"empty_input", ``, ``},
	{"no_body_wrapper", `x <b>y</b>`, `x **y**`},
}

func TestRichTextGolden(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "*.html"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 5 {
		t.Fatalf("want at least the 5 seed cases in testdata, found %d files", len(files))
	}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".html")
		t.Run(name, func(t *testing.T) {
			in, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(strings.TrimSuffix(f, ".html") + ".md")
			if err != nil {
				t.Fatal(err)
			}
			got := ToMarkdown(strings.TrimSuffix(string(in), "\n"), testUsers)
			if exp := strings.TrimSuffix(string(want), "\n"); got != exp {
				t.Errorf("got  %q\nwant %q", got, exp)
			}
		})
	}
	for _, tc := range goldenCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ToMarkdown(tc.in, testUsers); got != tc.want {
				t.Errorf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestToMarkdownNilResolver(t *testing.T) {
	in := `<a data-asana-gid="1" data-asana-type="user"></a>`
	if got := ToMarkdown(in, nil); got != "@unknown user" {
		t.Errorf("got %q", got)
	}
}
