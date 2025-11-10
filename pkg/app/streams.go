package app

import (
// "io"
// "os"
// std "github.com/jlrickert/go-std/pkg"
)

// // Streams holds the IO streams used by the application and optional hook
// // functions tests can override. Production code should call the helper
// // methods (IsStdinPiped, IsStdoutPiped, IsStdoutTTY) instead of performing
// // raw terminal detection directly.
// type Streams struct {
// 	In  io.Reader
// 	Out io.Writer
// 	Err io.Writer
//
// 	// Optional hook functions. If non-nil they are invoked when the
// 	// corresponding helper is called. If nil the helper attempts a
// 	// reasonable fallback using the concrete In/Out types.
// 	IsStdinPiped  bool
// 	IsStdoutPiped bool
// 	IsStdoutTTY   bool
// }
//
// // ConstBool returns a function that always returns the provided bool.
// // Useful for tests and convenience option helpers.
// func ConstBool(fn bool) func() bool { return func() bool { return fn } }

// // IsStdinPiped reports whether stdin appears to be piped. Behavior:
// // - If IsStdinPipedFn is non-nil, call and return it.
// // - Else, if In is an *os.File, use std.StdinHasData(f).
// // - Else return false.
// func (s Streams) IsStdinPiped() bool {
// 	if s.IsStdinPipedFn != nil {
// 		return s.IsStdinPipedFn()
// 	}
// 	if f, ok := s.In.(*os.File); ok && f != nil {
// 		return std.StdinHasData(f)
// 	}
// 	return false
// }
//
// // IsStdoutTTY reports whether stdout is an interactive terminal. Behavior:
// // - If IsStdoutTTYFn is non-nil, call and return it.
// // - Else, if Out is an *os.File, use std.IsInteractiveTerminal(f).
// // - Else return false.
// func (s Streams) IsStdoutTTY() bool {
// 	if s.IsStdoutTTYFn != nil {
// 		return s.IsStdoutTTYFn()
// 	}
// 	if f, ok := s.Out.(*os.File); ok && f != nil {
// 		return std.IsInteractiveTerminal(f)
// 	}
// 	return false
// }
//
// // IsStdoutPiped reports whether stdout appears to be piped or redirected.
// // Behavior:
// // - If IsStdoutPipedFn is non-nil, call and return it.
// // - Else, if Out is an *os.File, return the inverse of IsStdoutTTY.
// // - Else return false.
// func (s Streams) IsStdoutPiped() bool {
// 	if s.IsStdoutPipedFn != nil {
// 		return s.IsStdoutPipedFn()
// 	}
// 	if f, ok := s.Out.(*os.File); ok && f != nil {
// 		return !std.IsInteractiveTerminal(f)
// 	}
// 	return false
// }
