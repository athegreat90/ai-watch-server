// Package doccheck enforces the project's Go documentation standard in `go test`.
//
// Every package under cmd and internal must have a package comment, and every function,
// method and type declared in non-test files, exported or not, must carry a doc comment that
// begins with its own name (https://go.dev/doc/comment).
package doccheck

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// roots are the source trees checked, relative to this package directory.
var roots = []string{"../../cmd", "../../internal"}

// docStartsWith reports whether the doc comment group begins with name, as godoc requires.
func docStartsWith(doc *ast.CommentGroup, name string) bool {
	if doc == nil {
		return false
	}
	text := strings.TrimSpace(doc.Text())
	return text == name || strings.HasPrefix(text, name+" ") || strings.HasPrefix(text, name+"\n") ||
		strings.HasPrefix(text, name+",") || strings.HasPrefix(text, name+"'")
}

// receiverName returns "Type" for a method receiver such as (t *Type), or "" for plain functions.
func receiverName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return ""
	}
	expr := fn.Recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	if index, ok := expr.(*ast.IndexExpr); ok { // generic receiver T[P]
		expr = index.X
	}
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	return ""
}

// checkFile appends a message for every undocumented or mis-documented declaration in file.
func checkFile(fset *token.FileSet, file *ast.File, problems *[]string) {
	where := func(p token.Pos) string { return fset.Position(p).String() }
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if !docStartsWith(d.Doc, d.Name.Name) {
				kind := "function"
				if receiverName(d) != "" {
					kind = "method " + receiverName(d) + "."
				}
				*problems = append(*problems, where(d.Pos())+": "+kind+d.Name.Name+" needs a doc comment starting with its name")
			}
		case *ast.GenDecl:
			if d.Tok != token.TYPE {
				continue
			}
			for _, spec := range d.Specs {
				ts := spec.(*ast.TypeSpec)
				doc := ts.Doc
				if doc == nil && len(d.Specs) == 1 {
					doc = d.Doc
				}
				if !docStartsWith(doc, ts.Name.Name) {
					*problems = append(*problems, where(ts.Pos())+": type "+ts.Name.Name+" needs a doc comment starting with its name")
				}
			}
		}
	}
}

// TestAllFunctionsDocumented fails when a package lacks a package comment or when any function,
// method or type in non-test files lacks a doc comment that begins with its name.
func TestAllFunctionsDocumented(t *testing.T) {
	var problems []string
	packageDoc := map[string]bool{} // directory -> has a package comment
	checked := 0
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
			if err != nil {
				return err
			}
			checked++
			dir := filepath.Dir(path)
			if _, seen := packageDoc[dir]; !seen {
				packageDoc[dir] = false
			}
			if file.Doc != nil {
				prefix := "Package " + file.Name.Name
				if file.Name.Name == "main" {
					prefix = "Command "
				}
				if strings.HasPrefix(strings.TrimSpace(file.Doc.Text()), prefix) {
					packageDoc[dir] = true
				}
			}
			checkFile(fset, file, &problems)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if checked == 0 {
		t.Fatal("no Go files found: the doccheck roots are wrong")
	}
	for dir, ok := range packageDoc {
		if !ok {
			problems = append(problems, dir+": package needs a package comment (\"// Package x ...\" or \"// Command x ...\")")
		}
	}
	for _, p := range problems {
		t.Error(p)
	}
}

// TestDocStartsWith covers the comment-matching rules of the checker itself.
func TestDocStartsWith(t *testing.T) {
	group := func(s string) *ast.CommentGroup {
		return &ast.CommentGroup{List: []*ast.Comment{{Text: "// " + s}}}
	}
	tests := []struct {
		name string
		doc  *ast.CommentGroup
		want bool
	}{
		{"missing", nil, false},
		{"exact name", group("Run"), true},
		{"name then text", group("Run starts it."), true},
		{"other name", group("Starts the run."), false},
		{"prefix of longer name", group("Runner does things."), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := docStartsWith(tc.doc, "Run"); got != tc.want {
				t.Fatalf("got %v", got)
			}
		})
	}
}
