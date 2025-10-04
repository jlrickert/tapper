package cmd_test

//
// import (
// 	"bytes"
// 	"context"
// 	"strings"
// 	"testing"
//
// 	"github.com/jlrickert/tapper/pkg/keg"
// 	"github.com/jlrickert/tapper/pkg/keg/cmd"
// )
//
// func TestRun_HelpSucceeds(t *testing.T) {
// 	f := NewTestFixture(t)
// 	err := f.Run([]string{"--help"})
// 	if err != nil {
// 		t.Fatalf("Run returned error: %v", err)
// 	}
//
// 	got := f.Stdout()
// 	if !(strings.Contains(got, "Usage") || strings.Contains(got, "help")) {
// 		t.Fatalf("expected help output to contain Usage/help; got: %q", got)
// 	}
//
// 	// var out bytes.Buffer
// 	//
// 	// mem := keg.NewMemoryRepo()
// 	// k := keg.NewKeg(mem, nil)
// 	//
// 	// // Request help output from the root command.
// 	// if err := cmd.Run(t.Context(), []string{"--help"},
// 	// 	cmd.WithIO(nil, &out, &out),
// 	// 	cmd.WithKeg(k),
// 	// ); err != nil {
// 	// 	t.Fatalf("Run returned error: %v", err)
// 	// }
// 	//
// 	// got := out.String()
// 	// if !(strings.Contains(got, "Usage") || strings.Contains(got, "help")) {
// 	// 	t.Fatalf("expected help output to contain Usage/help; got: %q", got)
// 	// }
// }
//
// func TestRun_UnknownCommandReturnsError(t *testing.T) {
// 	var out bytes.Buffer
//
// 	mem := keg.NewMemoryRepo()
// 	k := keg.NewKeg(mem, nil)
//
// 	ctx := context.Background()
// 	// Run with an unknown subcommand and expect an error.
// 	err := cmd.Run(ctx, []string{"no-such-cmd"}, cmd.WithIO(nil, &out, &out), cmd.WithKeg(k))
// 	if err == nil {
// 		t.Fatalf("expected error for unknown command, got nil; output: %q", out.String())
// 	}
// }
