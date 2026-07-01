package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

func TestHandleEditSessionDialogKeySavesBeforePostCommit(t *testing.T) {
	body := readUIFunctionBody(t, "home.go", "handleEditSessionDialogKey")
	saveIdx := strings.Index(body, "h.forceSaveInstances()")
	postIdx := strings.Index(body, "for _, fn := range postCommits")
	if saveIdx < 0 || postIdx < 0 || saveIdx > postIdx {
		t.Fatalf("handleEditSessionDialogKey must save before postCommit; save=%d post=%d", saveIdx, postIdx)
	}
}

func TestWebMutatorUpdateSessionSavesBeforePostCommit(t *testing.T) {
	body := readUIFunctionBody(t, "web_mutator.go", "UpdateSession")
	saveIdx := strings.Index(body, "storage.SaveWithGroups")
	postIdx := strings.Index(body, "for _, fn := range postCommits")
	if saveIdx < 0 || postIdx < 0 || saveIdx > postIdx {
		t.Fatalf("UpdateSession must save before postCommit; save=%d post=%d", saveIdx, postIdx)
	}
}

func readUIFunctionBody(t *testing.T, filename, funcName string) string {
	t.Helper()
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("read %s: %v", filename, err)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, data, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", filename, err)
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != funcName {
			continue
		}
		start := fset.Position(fn.Body.Pos()).Offset
		end := fset.Position(fn.Body.End()).Offset
		return string(data[start:end])
	}
	t.Fatalf("function %s not found in %s", funcName, filename)
	return ""
}
