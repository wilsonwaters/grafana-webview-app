package htmlparse

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// dumpTree writes a canonical, line-per-node description of the tree rooted at
// n, so two trees can be compared without depending on the renderer.
func dumpTree(b *strings.Builder, n *html.Node, depth int) {
	fmt.Fprintf(b, "%s%d|%s|%q|", strings.Repeat(" ", depth), n.Type, n.Namespace, n.Data)
	for _, at := range n.Attr {
		fmt.Fprintf(b, "%s:%s=%q ", at.Namespace, at.Key, at.Val)
	}
	b.WriteByte('\n')
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Parent != n {
			fmt.Fprintf(b, "%sBROKEN PARENT LINK\n", strings.Repeat(" ", depth+1))
		}
		dumpTree(b, c, depth+1)
	}
}

func parseBoth(t *testing.T, input string, lim Limits) (upstream, ours string) {
	t.Helper()
	var ub, ob strings.Builder
	if doc, err := html.Parse(strings.NewReader(input)); err != nil {
		ub.WriteString("error: " + err.Error())
	} else {
		dumpTree(&ub, doc, 0)
	}
	if doc, err := ParseDocument(strings.NewReader(input), lim); err != nil {
		ob.WriteString("error: " + err.Error())
	} else {
		dumpTree(&ob, doc, 0)
	}
	return ub.String(), ob.String()
}

// moduleDir returns the directory of the golang.org/x/net module that go.mod
// selects, or skips the test when the go command cannot report it.
func moduleDir(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "golang.org/x/net").Output()
	if err != nil {
		t.Skipf("go list golang.org/x/net: %v", err)
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" {
		t.Skip("golang.org/x/net module directory not available")
	}
	return dir
}

// html5libDocuments returns the #data section of every document (not
// fragment) test in the html5lib tree-construction .dat files.
func html5libDocuments(t *testing.T, dir string) map[string]string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "html", "testdata", "html5lib-tests", "tree-construction", "*.dat"))
	if err != nil || len(files) == 0 {
		t.Skipf("no html5lib tree-construction tests under %s", dir)
	}
	docs := map[string]string{}
	for _, file := range files {
		f, err := os.Open(file)
		if err != nil {
			t.Fatal(err)
		}
		var (
			data     []string
			inData   bool
			fragment bool
			n        int
		)
		flush := func() {
			if data != nil && !fragment {
				docs[fmt.Sprintf("%s#%d", filepath.Base(file), n)] = strings.Join(data, "\n")
			}
			data, fragment = nil, false
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "#data":
				flush()
				n++
				inData, data = true, []string{}
			case strings.HasPrefix(line, "#"):
				if line == "#document-fragment" {
					fragment = true
				}
				inData = false
			case inData:
				data = append(data, line)
			}
		}
		flush()
		_ = f.Close()
		if err := sc.Err(); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
	}
	return docs
}

// TestParseMatchesUpstream checks that ParseDocument builds exactly the tree
// that golang.org/x/net/html.Parse builds, for every html5lib document test of
// the selected golang.org/x/net version and for the adversarial inputs below.
// A failure after a golang.org/x/net upgrade means the copied tree builder
// must be updated (see the package documentation).
func TestParseMatchesUpstream(t *testing.T) {
	docs := html5libDocuments(t, moduleDir(t))
	extra := map[string]string{
		"formatting reconstruction": "<p>" + strings.Repeat("<b x>", 10) + strings.Repeat("<p>x", 20),
		"ignored end tags":          "<body>" + strings.Repeat("x</q>", 50),
		"deep nesting":              strings.Repeat("<div>", 600),
		"table foster parenting":    "<table>" + strings.Repeat("x<td>y</td>", 20) + "</table>",
		"svg and math":              "<svg><style><b>x</b></style><foreignObject><p>y</foreignObject></svg><math><mi>z</math>",
		"repeated body attributes":  strings.Repeat("<body a b c>", 5) + strings.Repeat("<html d e>", 5),
	}
	for name, in := range extra {
		docs["extra: "+name] = in
	}
	if len(docs) < 1000 {
		t.Fatalf("found only %d documents; the html5lib corpus did not load", len(docs))
	}
	t.Logf("comparing %d documents", len(docs))
	// The same trees with no limits and with limits that every test document
	// fits in: the budget hooks must not change the tree.
	for name, lim := range map[string]Limits{
		"no limits":   {},
		"with limits": {MaxTokenBytes: 1 << 20, MaxTreeBytes: 8 << 20, MaxWork: 8 << 20},
	} {
		t.Run(name, func(t *testing.T) {
			mismatches := 0
			for doc, in := range docs {
				up, ours := parseBoth(t, in, lim)
				if up != ours {
					mismatches++
					if mismatches <= 5 {
						t.Errorf("%s: trees differ for %q\nupstream:\n%s\nours:\n%s", doc, in, up, ours)
					}
				}
			}
			if mismatches > 0 {
				t.Fatalf("%d of %d documents differ", mismatches, len(docs))
			}
		})
	}
}

// reconstructionBomb returns a page of about size bytes that makes the tree
// builder clone 42 formatting elements, each with attrs attributes, for every
// "<p>x": 14 formatting tag names, three of each with distinct attributes.
func reconstructionBomb(size, attrs int) string {
	var b strings.Builder
	b.WriteString("<!doctype html><body><p>")
	for _, tag := range []string{"a", "b", "big", "code", "em", "font", "i", "nobr", "s", "small", "strike", "strong", "tt", "u"} {
		for k := 0; k < 3; k++ {
			b.WriteString("<" + tag)
			for i := 0; i < attrs; i++ {
				fmt.Fprintf(&b, " x%d=%d", i, k)
			}
			b.WriteString(">")
		}
	}
	for b.Len() < size {
		b.WriteString("<p>x")
	}
	return b.String()
}

// TestParseDocumentStopsAtLimits checks that each kind of runaway parse stops
// with ErrLimitExceeded, and that an ordinary document within the same limits
// parses.
func TestParseDocumentStopsAtLimits(t *testing.T) {
	lim := Limits{MaxTokenBytes: 64 << 10, MaxTreeBytes: 4 << 20, MaxWork: 4 << 20}
	cases := map[string]string{
		// About 300 bytes of input per 42 cloned elements: the tree budget.
		"formatting reconstruction": reconstructionBomb(256<<10, 4),
		// Each "x" is appended to one text node: quadratic copying, the work
		// budget.
		"text appends": "<body>" + strings.Repeat("x</q>", 20000),
		// One tag of more than MaxTokenBytes: the token limit.
		"huge tag": "<p " + strings.Repeat("a=1 ", 20000) + ">",
		// Many small elements: the tree budget.
		"dense elements": "<body>" + strings.Repeat("<br>", 100000),
		// Repeated <body> tags whose attributes are merged: the work budget.
		"body attribute merging": func() string {
			var b strings.Builder
			for i := 0; i < 3000; i++ {
				fmt.Fprintf(&b, "<body a%d>", i)
			}
			return b.String()
		}(),
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseDocument(strings.NewReader(in), lim)
			if !errors.Is(err, ErrLimitExceeded) {
				t.Fatalf("err = %v, want ErrLimitExceeded", err)
			}
		})
	}

	ordinary := "<!doctype html><html><head><title>t</title></head><body>" +
		strings.Repeat(`<div class="row"><a href="/x"><b>bold</b> text</a><p>para</p></div>`, 500) + "</body></html>"
	if _, err := ParseDocument(strings.NewReader(ordinary), lim); err != nil {
		t.Fatalf("ordinary document: %v", err)
	}

	// A parse error that is not a limit keeps its own error.
	if _, err := ParseDocument(strings.NewReader(strings.Repeat("<div>", 600)), lim); err == nil || errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("deep nesting err = %v, want the parser's own error", err)
	}
}
