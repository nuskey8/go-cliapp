package cliapp

import (
	"bytes"
	"fmt"
	"os"
	"testing"
)

func TestAddCommand(t *testing.T) {
	app := New(Options{ExitOnError: false})
	// handler prints sum to stdout
	app.Add("add", func(a int, b int) {
		fmt.Printf("%d", a+b)
	})

	// capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := app.Run("add", "2", "3")
	w.Close()
	os.Stdout = oldStdout
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	var out bytes.Buffer
	_, _ = out.ReadFrom(r)
	got := out.String()
	if got != "5" {
		t.Fatalf("expected '5', got %q", got)
	}
}

func TestMissingArg(t *testing.T) {
	app := New(Options{ExitOnError: false})
	app.Add("echo", func(s string) {})
	err := app.Run("echo")
	if err == nil {
		t.Fatalf("expected error for missing arg")
	}
}

func TestStructArgParsing(t *testing.T) {
	type CreateTextArgs struct {
		Input       string `arg:"0"`
		Output      string `long:"--out" short:"-o"`
		UseMarkdown bool   `flag:"" long:"--usemarkdown"`
	}

	app := New(Options{ExitOnError: false})
	var got CreateTextArgs
	app.Add("create", func(a CreateTextArgs) {
		got = a
	})

	err := app.Run("create", "hello.txt", "--out=out.txt", "--usemarkdown")
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if got.Input != "hello.txt" {
		t.Fatalf("expected Input hello.txt, got %q", got.Input)
	}
	if got.Output != "out.txt" {
		t.Fatalf("expected Output out.txt, got %q", got.Output)
	}
	if got.UseMarkdown != true {
		t.Fatalf("expected UseMarkdown true, got %v", got.UseMarkdown)
	}
}
