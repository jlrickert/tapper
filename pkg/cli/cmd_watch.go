package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

// watchEventJSON is the NDJSON shape emitted by `tap watch --json`. It mirrors
// keg.NodeEvent plus an observation timestamp.
type watchEventJSON struct {
	Kind  string `json:"kind"`
	Node  string `json:"node"`
	Field string `json:"field,omitempty"`
	Ts    string `json:"ts"`
}

// NewWatchCmd returns the `watch` cobra command.
func NewWatchCmd(deps *Deps) *cobra.Command {
	var opts tapper.WatchNodeOptions
	var jsonOut bool
	var includeAccess bool
	var count int
	var timeout time.Duration

	cmd := &cobra.Command{
		Use:               "watch NODE_ID",
		Short:             "stream live change events for a node",
		ValidArgsFunction: nodeIDCompletionFunc(deps, 1),
		Long: `Stream live change events for a node until interrupted.

Each change to the node prints one line describing what changed: the event
kind (created, modified, deleted), the node ID, and the affected field
(content, meta, stats). Access events are suppressed unless --all is given.

For filesystem kegs the command watches the node directory; for hub kegs it
subscribes to the hub's live event stream, so saves made by the web UI or by
other tap instances appear here as they happen.

Use --json for newline-delimited JSON suitable for scripting, --count to exit
after a fixed number of events, and --timeout to exit after a duration.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.NodeID = args[0]
			applyKegTargetProfile(deps, &opts.KegTargetOptions)

			ctx := cmd.Context()
			if timeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, timeout)
				defer cancel()
			}

			// The watch is ctx-scoped: cancelling ctx (timeout, interrupt)
			// closes the channel and releases all watch resources.
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			ch, err := deps.Tap.WatchNode(ctx, opts)
			if err != nil {
				return err
			}

			out := deps.Runtime.Stream().Out
			seen := 0
			for {
				select {
				case <-ctx.Done():
					// Timeout or interrupt is the normal way to stop
					// watching, not a failure.
					return nil
				case ev, ok := <-ch:
					if !ok {
						return nil
					}
					if ev.Kind == keg.NodeEventAccessed && !includeAccess {
						continue
					}
					if err := writeWatchEvent(out, deps.Runtime.Clock().Now(), ev, jsonOut); err != nil {
						return err
					}
					seen++
					if count > 0 && seen >= count {
						return nil
					}
				}
			}
		},
	}

	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit newline-delimited JSON events")
	cmd.Flags().BoolVar(&includeAccess, "all", false, "include access events (suppressed by default)")
	cmd.Flags().IntVar(&count, "count", 0, "exit after this many events (0 = unlimited)")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "exit after this duration (0 = run until interrupted)")
	return cmd
}

func writeWatchEvent(out io.Writer, now time.Time, ev keg.NodeEvent, jsonOut bool) error {
	ts := now.UTC().Format(time.RFC3339)
	if jsonOut {
		raw, err := json.Marshal(watchEventJSON{
			Kind:  ev.Kind.String(),
			Node:  ev.NodeID.String(),
			Field: ev.Field,
			Ts:    ts,
		})
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "%s\n", raw)
		return err
	}
	if ev.Field != "" {
		_, err := fmt.Fprintf(out, "%s  node %s  %s  %s\n", ts, ev.NodeID.String(), ev.Kind, ev.Field)
		return err
	}
	_, err := fmt.Fprintf(out, "%s  node %s  %s\n", ts, ev.NodeID.String(), ev.Kind)
	return err
}
