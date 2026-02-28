package keg

import (
	"encoding/json"
	"fmt"
	"strings"
)

const snapshotPatchAlgorithm = "line-patch-v1"

type textPatch struct {
	BaseHash string        `json:"base_hash,omitempty"`
	Ops      []textPatchOp `json:"ops"`
}

type textPatchOp struct {
	Type  string   `json:"type"`
	Count int      `json:"count,omitempty"`
	Lines []string `json:"lines,omitempty"`
}

func buildSnapshotPatch(rt anySnapshotHasher, base []byte, target []byte) ([]byte, error) {
	baseLines := splitSnapshotLines(base)
	targetLines := splitSnapshotLines(target)
	ops := diffSnapshotLines(baseLines, targetLines)
	out, err := json.Marshal(textPatch{
		BaseHash: hashSnapshotWith(rt, base),
		Ops:      ops,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal snapshot patch: %w", err)
	}
	return out, nil
}

func applySnapshotPatch(rt anySnapshotHasher, base []byte, patchBytes []byte) ([]byte, error) {
	var patch textPatch
	if err := json.Unmarshal(patchBytes, &patch); err != nil {
		return nil, fmt.Errorf("parse snapshot patch: %w", err)
	}
	if patch.BaseHash != "" && patch.BaseHash != hashSnapshotWith(rt, base) {
		return nil, fmt.Errorf("snapshot patch base hash mismatch: %w", ErrConflict)
	}

	baseLines := splitSnapshotLines(base)
	var out strings.Builder
	index := 0

	for _, op := range patch.Ops {
		switch op.Type {
		case "equal":
			if index+op.Count > len(baseLines) {
				return nil, fmt.Errorf("snapshot patch equal range out of bounds: %w", ErrInvalid)
			}
			for _, line := range baseLines[index : index+op.Count] {
				out.WriteString(line)
			}
			index += op.Count
		case "delete":
			if index+op.Count > len(baseLines) {
				return nil, fmt.Errorf("snapshot patch delete range out of bounds: %w", ErrInvalid)
			}
			index += op.Count
		case "insert":
			for _, line := range op.Lines {
				out.WriteString(line)
			}
		default:
			return nil, fmt.Errorf("unknown snapshot patch op %q: %w", op.Type, ErrInvalid)
		}
	}

	if index != len(baseLines) {
		return nil, fmt.Errorf("snapshot patch did not consume base content: %w", ErrInvalid)
	}
	return []byte(out.String()), nil
}

type anySnapshotHasher interface {
	Hash(data []byte) string
}

func hashSnapshotWith(hasher anySnapshotHasher, data []byte) string {
	if hasher == nil || len(data) == 0 {
		return ""
	}
	return hasher.Hash(data)
}

func splitSnapshotLines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	lines := strings.SplitAfter(string(data), "\n")
	if len(lines) == 0 {
		return nil
	}
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func diffSnapshotLines(base []string, target []string) []textPatchOp {
	if len(base) == 0 && len(target) == 0 {
		return nil
	}

	dp := make([][]int, len(base)+1)
	for i := range dp {
		dp[i] = make([]int, len(target)+1)
	}

	for i := len(base) - 1; i >= 0; i-- {
		for j := len(target) - 1; j >= 0; j-- {
			if base[i] == target[j] {
				dp[i][j] = dp[i+1][j+1] + 1
				continue
			}
			if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	var ops []textPatchOp
	appendEqual := func(count int) {
		if count == 0 {
			return
		}
		if len(ops) > 0 && ops[len(ops)-1].Type == "equal" {
			ops[len(ops)-1].Count += count
			return
		}
		ops = append(ops, textPatchOp{Type: "equal", Count: count})
	}
	appendDelete := func(count int) {
		if count == 0 {
			return
		}
		if len(ops) > 0 && ops[len(ops)-1].Type == "delete" {
			ops[len(ops)-1].Count += count
			return
		}
		ops = append(ops, textPatchOp{Type: "delete", Count: count})
	}
	appendInsert := func(line string) {
		if len(ops) > 0 && ops[len(ops)-1].Type == "insert" {
			ops[len(ops)-1].Lines = append(ops[len(ops)-1].Lines, line)
			return
		}
		ops = append(ops, textPatchOp{Type: "insert", Lines: []string{line}})
	}

	i, j := 0, 0
	for i < len(base) && j < len(target) {
		switch {
		case base[i] == target[j]:
			appendEqual(1)
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			appendDelete(1)
			i++
		default:
			appendInsert(target[j])
			j++
		}
	}

	for i < len(base) {
		appendDelete(1)
		i++
	}
	for j < len(target) {
		appendInsert(target[j])
		j++
	}

	return compactSnapshotPatchOps(ops)
}

func compactSnapshotPatchOps(ops []textPatchOp) []textPatchOp {
	if len(ops) == 0 {
		return nil
	}
	out := make([]textPatchOp, 0, len(ops))
	for _, op := range ops {
		if len(out) == 0 {
			out = append(out, cloneSnapshotPatchOp(op))
			continue
		}
		last := &out[len(out)-1]
		if last.Type != op.Type {
			out = append(out, cloneSnapshotPatchOp(op))
			continue
		}
		switch op.Type {
		case "equal", "delete":
			last.Count += op.Count
		case "insert":
			last.Lines = append(last.Lines, op.Lines...)
		default:
			out = append(out, cloneSnapshotPatchOp(op))
		}
	}
	return out
}

func cloneSnapshotPatchOp(op textPatchOp) textPatchOp {
	out := textPatchOp{
		Type:  op.Type,
		Count: op.Count,
	}
	if len(op.Lines) > 0 {
		out.Lines = append([]string(nil), op.Lines...)
	}
	return out
}
