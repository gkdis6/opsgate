// Command site renders the README and the docs into a static site for GitHub
// Pages, so the project has one crawlable home with stable URLs, page
// descriptions, a sitemap and llms.txt. It runs in CI; see
// .github/workflows/pages.yml.
//
//	go run . -root ../.. -out ../../_site
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
)

const (
	repo    = "https://github.com/gkdis6/opsgate"
	tagline = "Kubernetes MCP server with human approval: AI proposes, you approve each change with a passkey from your phone."
)

func main() {
	root := flag.String("root", ".", "repository root")
	out := flag.String("out", "_site", "output directory")
	base := flag.String("base", "https://gkdis6.github.io/opsgate", "public site URL without a trailing slash")
	flag.Parse()
	if err := build(*root, *out, strings.TrimRight(*base, "/")); err != nil {
		log.Fatal(err)
	}
}

// page is one Markdown source and the HTML path it becomes.
type page struct{ src, dst string }

func pages(root string) ([]page, error) {
	list := []page{{"README.md", "index.html"}, {"README.ko.md", "ko/index.html"}, {"SECURITY.md", "security.html"}, {"CHANGELOG.md", "changelog.html"}}
	err := filepath.WalkDir(filepath.Join(root, "docs"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".md") {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		list = append(list, page{rel, strings.TrimSuffix(rel, ".md") + ".html"})
		return nil
	})
	return list, err
}

// repoRoot lets rewrite tell files from directories when linking to GitHub.
var repoRoot string

func build(root, out, base string) error {
	repoRoot = root
	list, err := pages(root)
	if err != nil {
		return err
	}
	dst := map[string]string{}
	for _, p := range list {
		dst[p.src] = p.dst
	}
	if err := os.RemoveAll(out); err != nil {
		return err
	}
	var urls []string
	for _, p := range list {
		src, err := os.ReadFile(filepath.Join(root, p.src))
		if err != nil {
			return err
		}
		doc, err := render(src, p, dst, base)
		if err != nil {
			return fmt.Errorf("%s: %w", p.src, err)
		}
		if err := write(filepath.Join(out, p.dst), doc); err != nil {
			return err
		}
		urls = append(urls, base+"/"+strings.TrimSuffix(p.dst, "index.html"))
	}
	if err := copyTree(filepath.Join(root, "docs", "images"), filepath.Join(out, "docs", "images")); err != nil {
		return err
	}
	llms, err := os.ReadFile(filepath.Join(root, "llms.txt"))
	if err != nil {
		return err
	}
	if err := write(filepath.Join(out, "llms.txt"), llms); err != nil {
		return err
	}
	sort.Strings(urls)
	var sitemap bytes.Buffer
	sitemap.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n" + `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	for _, u := range urls {
		fmt.Fprintf(&sitemap, "  <url><loc>%s</loc></url>\n", template.HTMLEscapeString(u))
	}
	sitemap.WriteString("</urlset>\n")
	if err := write(filepath.Join(out, "sitemap.xml"), sitemap.Bytes()); err != nil {
		return err
	}
	if err := write(filepath.Join(out, "robots.txt"), []byte("User-agent: *\nAllow: /\nSitemap: "+base+"/sitemap.xml\n")); err != nil {
		return err
	}
	return write(filepath.Join(out, ".nojekyll"), nil)
}

var md = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithParserOptions(parser.WithASTTransformers()),
	// The sources are this repository's own documents, which use a little HTML.
	goldmark.WithRendererOptions(html.WithUnsafe()),
)

type view struct {
	Lang, Title, Description, Canonical, Root, Source, Other, OtherLabel string
	Body                                                                 template.HTML
	JSONLD                                                               template.JS
}

func render(src []byte, p page, dst map[string]string, base string) ([]byte, error) {
	doc := md.Parser().Parse(text.NewReader(src))
	title, description := "", ""
	ids := map[string]int{}
	err := ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.Heading:
			label := plain(n, src)
			if n.Level == 1 && title == "" {
				title = label
			}
			n.SetAttributeString("id", []byte(headingID(label, ids)))
		case *ast.Paragraph:
			if t := plain(n, src); description == "" && title != "" && len([]rune(t)) > 40 {
				description = t
			}
		case *ast.Link:
			n.Destination = []byte(rewrite(string(n.Destination), p, dst))
		case *ast.Image:
			n.Destination = []byte(rewrite(string(n.Destination), p, dst))
		case *ast.RawHTML, *ast.HTMLBlock:
			// Raw HTML keeps its own paths; the logo uses docs/images, which
			// is copied next to the README page as well.
		}
		return ast.WalkContinue, nil
	})
	if err != nil {
		return nil, err
	}
	var body bytes.Buffer
	if err := md.Renderer().Render(&body, src, doc); err != nil {
		return nil, err
	}
	html := body.String()
	rootRel := strings.Repeat("../", strings.Count(p.dst, "/"))
	if rootRel == "" {
		rootRel = "./"
	}
	if p.src == "README.md" || p.src == "README.ko.md" {
		// The README's raw <img> uses a repository-relative path.
		html = strings.ReplaceAll(html, `src="docs/images/`, `src="`+rootRel+`docs/images/`)
	}
	v := view{Lang: "en", Title: title + " · opsgate", Description: truncate(description, 300), Canonical: base + "/" + strings.TrimSuffix(p.dst, "index.html"),
		Root: rootRel, Source: repo + "/blob/main/" + p.src, Body: template.HTML(html)}
	if strings.HasPrefix(p.src, "docs/ko/") || p.src == "README.ko.md" {
		v.Lang = "ko"
	}
	switch p.src {
	case "README.md":
		v.Title, v.Description = "opsgate: Kubernetes MCP server with passkey approval", tagline
		v.Other, v.OtherLabel = "ko/", "한국어"
		ld, _ := json.Marshal(map[string]any{
			"@context": "https://schema.org", "@type": "SoftwareApplication", "name": "opsgate",
			"description": tagline, "applicationCategory": "DeveloperApplication", "operatingSystem": "Linux, macOS",
			"license": "https://www.apache.org/licenses/LICENSE-2.0", "codeRepository": repo, "url": base + "/",
			"programmingLanguage": "Go", "offers": map[string]any{"@type": "Offer", "price": "0", "priceCurrency": "USD"},
			"keywords": "MCP server, Kubernetes, human-in-the-loop, passkey, WebAuthn, approval, self-hosted, AI agents, Claude, ChatGPT",
		})
		v.JSONLD = template.JS(ld)
	case "README.ko.md":
		v.Title = "opsgate: AI가 제안하고 패스키가 실행하는 Kubernetes MCP 서버"
		v.Other, v.OtherLabel = "../", "English"
	}
	var out bytes.Buffer
	err = layout.Execute(&out, v)
	return out.Bytes(), err
}

// rewrite points a relative link at the rendered page, or at the file on
// GitHub when it is not a page. Absolute URLs and fragments stay as they are.
func rewrite(dest string, p page, dst map[string]string) string {
	u, err := url.Parse(dest)
	if err != nil || u.Scheme != "" || u.Host != "" || u.Path == "" || strings.HasPrefix(u.Path, "/") {
		return dest
	}
	target := path.Clean(path.Join(path.Dir(p.src), u.Path))
	if strings.HasPrefix(target, "../") {
		return dest
	}
	frag := ""
	if u.Fragment != "" {
		frag = "#" + u.EscapedFragment()
	}
	if html, ok := dst[target]; ok {
		return relative(p.dst, html) + frag
	}
	if strings.HasPrefix(target, "docs/images/") {
		return relative(p.dst, target)
	}
	kind := "blob"
	if info, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(target))); err == nil && info.IsDir() {
		kind = "tree"
	}
	return repo + "/" + kind + "/main/" + target + frag
}

// relative returns the path from one output page to another output file.
func relative(from, to string) string {
	rel, err := filepath.Rel(path.Dir(from), to)
	if err != nil {
		return to
	}
	rel = filepath.ToSlash(rel)
	if strings.HasSuffix(rel, "index.html") {
		rel = strings.TrimSuffix(rel, "index.html")
		if rel == "" {
			rel = "./"
		}
	}
	return rel
}

// headingID follows GitHub's anchors so links written for GitHub keep working:
// lower case, punctuation removed, spaces as hyphens, repeats numbered.
func headingID(label string, seen map[string]int) string {
	var b strings.Builder
	for _, r := range strings.ToLower(label) {
		switch {
		case unicode.IsLetter(r) || unicode.IsNumber(r) || r == '-' || r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		}
	}
	id := b.String()
	if n := seen[id]; n > 0 {
		seen[id] = n + 1
		return fmt.Sprintf("%s-%d", id, n)
	}
	seen[id] = 1
	return id
}

func plain(n ast.Node, src []byte) string {
	var b strings.Builder
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			switch c := c.(type) {
			case *ast.Text:
				b.Write(c.Segment.Value(src))
				if c.SoftLineBreak() || c.HardLineBreak() {
					b.WriteByte(' ')
				}
			case *ast.String:
				b.Write(c.Value)
			case *ast.CodeSpan:
				for child := c.FirstChild(); child != nil; child = child.NextSibling() {
					if t, ok := child.(*ast.Text); ok {
						b.Write(t.Segment.Value(src))
					}
				}
				return ast.WalkSkipChildren, nil
			}
		}
		return ast.WalkContinue, nil
	})
	return strings.Join(strings.Fields(b.String()), " ")
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

func write(p string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

func copyTree(from, to string) error {
	return filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(from, p)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return write(filepath.Join(to, rel), data)
	})
}

var layout = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="{{.Lang}}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<meta name="description" content="{{.Description}}">
<link rel="canonical" href="{{.Canonical}}">
<meta property="og:title" content="{{.Title}}">
<meta property="og:description" content="{{.Description}}">
<meta property="og:url" content="{{.Canonical}}">
<meta property="og:image" content="https://raw.githubusercontent.com/gkdis6/opsgate/main/docs/images/social-preview.png">
<link rel="icon" href="{{.Root}}docs/images/icon.png">
{{if .JSONLD}}<script type="application/ld+json">{{.JSONLD}}</script>{{end}}
<style>
:root{--bg:#fff;--fg:#1f2328;--muted:#59636e;--line:#d1d9e0;--code:#f6f8fa;--link:#0969da}
@media (prefers-color-scheme:dark){:root{--bg:#0d1117;--fg:#e6edf3;--muted:#9198a1;--line:#3d444d;--code:#151b23;--link:#4493f8}}
body{margin:0;background:var(--bg);color:var(--fg);font:16px/1.6 -apple-system,BlinkMacSystemFont,"Segoe UI","Noto Sans KR",Helvetica,Arial,sans-serif}
header,main,footer{max-width:900px;margin:0 auto;padding:0 16px}
header{display:flex;gap:16px;align-items:center;padding-top:16px;padding-bottom:8px;border-bottom:1px solid var(--line);flex-wrap:wrap}
header strong a{color:var(--fg)}
a{color:var(--link);text-decoration:none}a:hover{text-decoration:underline}
pre,code{background:var(--code);border-radius:6px;font:14px ui-monospace,SFMono-Regular,Menlo,monospace}
code{padding:.1em .3em}pre{padding:12px;overflow-x:auto}pre code{padding:0}
table{border-collapse:collapse;display:block;overflow-x:auto}th,td{border:1px solid var(--line);padding:6px 12px;vertical-align:top}
img{max-width:100%}blockquote{margin:0;padding:0 16px;color:var(--muted);border-left:4px solid var(--line)}
footer{color:var(--muted);font-size:14px;border-top:1px solid var(--line);margin-top:48px;padding-top:12px;padding-bottom:24px}
</style>
</head>
<body>
<header><strong><a href="{{.Root}}">opsgate</a></strong><a href="{{.Root}}docs/approval.html">Approval</a><a href="{{.Root}}docs/watch.html">Watch</a><a href="{{.Root}}docs/tools.html">Tools</a><a href="{{.Root}}docs/configuration.html">Configuration</a><a href="https://github.com/gkdis6/opsgate">GitHub</a>{{if .Other}}<a href="{{.Other}}">{{.OtherLabel}}</a>{{end}}</header>
<main>
{{.Body}}
</main>
<footer>Apache-2.0 · <a href="{{.Source}}">Edit this page on GitHub</a> · <a href="{{.Root}}llms.txt">llms.txt</a></footer>
</body>
</html>
`))
