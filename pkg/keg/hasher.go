package keg

// import (
// 	"bytes"
// 	"context"
// 	"crypto/md5"
// 	"fmt"
// )
//
// // Hasher computes a deterministic short hash for a byte slice. Implementations
// // should return a textual representation suitable for inclusion in meta fields.
// type Hasher interface {
// 	Hash(data []byte) string
// }
//
// // MD5Hasher is a simple Hasher implementation that returns an MD5 hex digest.
// //
// // Note: MD5 is used here for deterministic, compact hashes only and is not
// // intended for cryptographic integrity protection.
// type MD5Hasher struct{}
//
// // Hash implements Hasher by returning the lowercase hex MD5 of the trimmed
// // input bytes.
// func (m *MD5Hasher) Hash(data []byte) string {
// 	sum := md5.Sum(bytes.TrimSpace(data))
// 	return fmt.Sprintf("%x", sum[:])
// }
//
// // DefaultHasher is the fallback hasher used when none is provided via context.
// var DefaultHasher Hasher = &MD5Hasher{}
//
// // context key type to avoid collisions
// type hasherKey struct{}
//
// // WithHasher returns a copy of ctx that carries the provided Hasher.
// // Use this to inject a custom hasher for tests or alternative hashing strategies.
// func WithHasher(ctx context.Context, h Hasher) context.Context {
// 	return context.WithValue(ctx, hasherKey{}, h)
// }
//
// // HasherFromContext returns the Hasher stored in ctx. If ctx is nil or does not
// // contain a Hasher, DefaultHasher is returned.
// func HasherFromContext(ctx context.Context) Hasher {
// 	if ctx == nil {
// 		return DefaultHasher
// 	}
// 	if v := ctx.Value(hasherKey{}); v != nil {
// 		if h, ok := v.(Hasher); ok && h != nil {
// 			return h
// 		}
// 	}
// 	return DefaultHasher
// }
//
// var _ Hasher = (*MD5Hasher)(nil)
