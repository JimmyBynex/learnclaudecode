package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/learnclaudecode/claude-go/src/phase-03-cache/query"
)

// helper to create a temp file with content and return its path.
func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "phase2-*.txt")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("WriteString: %v", err)
	}
	f.Close()
	return f.Name()
}

// ── TestExecuteToolValidationFail ─────────────────────────────────────────────

// TestExecuteToolValidationFail verifies that ExecuteTool returns an error
// (not calling Call) when ValidateInput fails.
func TestExecuteToolValidationFail(t *testing.T) {
	tool := NewBashTool()
	// Pass empty input — ValidateInput should reject it.
	input := map[string]any{} // missing "command"

	tctx := &query.ToolUseContext{}
	_, err := query.ExecuteTool(context.Background(), tool, input, tctx)
	if err == nil {
		t.Fatal("expected error from ExecuteTool when validation fails, got nil")
	}
	if !strings.Contains(err.Error(), "validation failed") {
		t.Errorf("expected 'validation failed' in error message, got: %v", err)
	}
}

// ── TestBashTool ──────────────────────────────────────────────────────────────

func TestBashTool(t *testing.T) {
	tool := NewBashTool()
	input := map[string]any{"command": "echo hello"}
	result, err := query.ExecuteTool(context.Background(), tool, input, &query.ToolUseContext{})
	if err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content)
	}
	if !strings.Contains(result.Content, "hello") {
		t.Errorf("expected 'hello' in output, got: %q", result.Content)
	}
}

// ── TestFileReadTool ──────────────────────────────────────────────────────────

func TestFileReadTool(t *testing.T) {
	content := "line one\nline two\nline three\n"
	path := writeTempFile(t, content)

	tool := NewFileReadTool()
	input := map[string]any{"file_path": path}
	result, err := query.ExecuteTool(context.Background(), tool, input, &query.ToolUseContext{})
	if err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content)
	}
	// Verify line-number prefix exists (format: "   1\tline one")
	if !strings.Contains(result.Content, "1\t") {
		t.Errorf("expected line number prefix '1\\t' in output, got: %q", result.Content)
	}
	if !strings.Contains(result.Content, "line one") {
		t.Errorf("expected 'line one' in output, got: %q", result.Content)
	}
	if !strings.Contains(result.Content, "3\t") {
		t.Errorf("expected line number '3\\t' in output, got: %q", result.Content)
	}
}

// ── TestFileWriteTool ─────────────────────────────────────────────────────────

func TestFileWriteTool(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "written.txt")
	wantContent := "hello from FileWriteTool\n"

	tool := NewFileWriteTool()
	input := map[string]any{
		"file_path": path,
		"content":   wantContent,
	}
	result, err := query.ExecuteTool(context.Background(), tool, input, &query.ToolUseContext{})
	if err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile: %v", err)
	}
	if string(got) != wantContent {
		t.Errorf("file content mismatch: want %q, got %q", wantContent, string(got))
	}
}

// ── TestFileEditTool ──────────────────────────────────────────────────────────

func TestFileEditTool(t *testing.T) {
	original := "Hello, World!\nThis is a test.\n"
	path := writeTempFile(t, original)

	editTool := NewFileEditTool()
	input := map[string]any{
		"file_path":  path,
		"old_string": "World",
		"new_string": "Go",
	}
	result, err := query.ExecuteTool(context.Background(), editTool, input, &query.ToolUseContext{})
	if err != nil {
		t.Fatalf("ExecuteTool (Edit): %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content)
	}

	// Verify with FileReadTool.
	readTool := NewFileReadTool()
	readResult, err := query.ExecuteTool(context.Background(), readTool, map[string]any{"file_path": path}, &query.ToolUseContext{})
	if err != nil {
		t.Fatalf("ExecuteTool (Read): %v", err)
	}
	if !strings.Contains(readResult.Content, "Hello, Go!") {
		t.Errorf("expected 'Hello, Go!' after edit, got: %q", readResult.Content)
	}
	if strings.Contains(readResult.Content, "Hello, World!") {
		t.Error("old string should have been replaced")
	}
}

// ── TestGlobTool ──────────────────────────────────────────────────────────────

func TestGlobTool(t *testing.T) {
	dir := t.TempDir()

	// Create some files.
	for _, name := range []string{"a.go", "b.go", "c.txt"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("content"), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}

	tool := NewGlobTool()
	input := map[string]any{
		"pattern": "*.go",
		"path":    dir,
	}
	result, err := query.ExecuteTool(context.Background(), tool, input, &query.ToolUseContext{})
	if err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content)
	}

	if !strings.Contains(result.Content, "a.go") {
		t.Errorf("expected 'a.go' in output, got: %q", result.Content)
	}
	if !strings.Contains(result.Content, "b.go") {
		t.Errorf("expected 'b.go' in output, got: %q", result.Content)
	}
	if strings.Contains(result.Content, "c.txt") {
		t.Errorf("unexpected 'c.txt' in glob *.go output, got: %q", result.Content)
	}
}

// ── TestGrepTool ──────────────────────────────────────────────────────────────

func TestGrepTool(t *testing.T) {
	content := "line one: apple\nline two: banana\nline three: apple pie\n"
	path := writeTempFile(t, content)

	tool := NewGrepTool()
	input := map[string]any{
		"pattern": "apple",
		"path":    path,
	}
	result, err := query.ExecuteTool(context.Background(), tool, input, &query.ToolUseContext{})
	if err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content)
	}

	if !strings.Contains(result.Content, "apple") {
		t.Errorf("expected 'apple' in grep output, got: %q", result.Content)
	}
	if strings.Contains(result.Content, "banana") {
		t.Errorf("unexpected 'banana' in grep output (should not match 'apple'), got: %q", result.Content)
	}
	// Check line number is present.
	if !strings.Contains(result.Content, ":1:") && !strings.Contains(result.Content, ":3:") {
		t.Errorf("expected line numbers in grep output, got: %q", result.Content)
	}
}
