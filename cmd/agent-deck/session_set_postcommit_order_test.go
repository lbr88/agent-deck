package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

func TestHandleSessionSetSavesBeforePostCommit(t *testing.T) {
	body := readFunctionBody(t, "session_cmd.go", "handleSessionSet")
	saveIdx := strings.Index(body, "storage.SaveWithGroups")
	postIdx := strings.Index(body, "postCommit()")
	if saveIdx < 0 || postIdx < 0 || saveIdx > postIdx {
		t.Fatalf("handleSessionSet must save before postCommit; save=%d post=%d", saveIdx, postIdx)
	}
}

func readFunctionBody(t *testing.T, filename, funcName string) string {
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
