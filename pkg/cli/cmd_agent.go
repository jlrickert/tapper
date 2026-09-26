package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jlrickert/tapper/pkg/tapper"
)

// NewAgentCmd returns `tap agent`: Hub agents, a namespace-owned model,
// instructions, tool allowlist, and memory flight. Each subcommand is the CLI peer of the agent_<verb> MCP tool.
//
//	tap agent list [--namespace @acme]
//	tap agent read @acme/researcher
//	tap agent create @acme/researcher --model relay/ollama/qwen3 --tool keg:read --flight @acme/+research
//	tap agent edit @acme/researcher --tool keg:read --tool keg:write
//	tap agent delete @acme/researcher
func NewAgentCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "read and manage Hub agents",
		Long: `Read and manage Hub agents. An agent is a model, a description, markdown
instructions, a tool allowlist, and a memory flight, owned by a namespace.
Members read a namespace's agents; owners and admins change them.

The agent is the unit of authority: its tools say what it may do, and its
flight (--flight @ns/+slug) is the memory it works in. An agent with no flight
has no KEG access. Apps and tokens bind to an agent, not a flight.

Tools are tool group ids (keg:read, keg:write, keg:admin, flight:read,
flight:admin, agent:read, agent:admin, discover) and single MCP tool names. No
tools means every tool.`,
	}
	cmd.AddCommand(
		newAgentListCmd(deps),
		newAgentReadCmd(deps),
		newAgentCreateCmd(deps),
		newAgentEditCmd(deps),
		newAgentDeleteCmd(deps),
	)
	return cmd
}

func newAgentListCmd(deps *Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "list agents as REF, TITLE, MODEL",
		Long: `List agents as REF, TITLE, MODEL. With --namespace, one namespace's
agents; without it, the agents of every namespace you belong to.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			target := globalKegTarget(deps)
			agents, err := deps.Tap.ListAgents(cmd.Context(), tapper.ListAgentsOptions{Namespace: target.Namespace, Hub: target.Hub})
			if err != nil {
				return err
			}
			for _, a := range agents {
				fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\n", a.Ref, a.Title, a.Model)
			}
			return nil
		},
	}
}

func newAgentReadCmd(deps *Deps) *cobra.Command {
	return &cobra.Command{
		Use:               "read <@namespace/name>",
		ValidArgsFunction: agentRefCompletion(deps),
		Short:             "show an agent's model, tools, flight and instructions",
		Args:              cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			agent, err := deps.Tap.GetAgent(cmd.Context(), tapper.GetAgentOptions{Ref: args[0], Hub: globalKegTarget(deps).Hub})
			if err != nil {
				return err
			}
			renderAgent(cmd.OutOrStdout(), agent)
			return nil
		},
	}
}

// agentFieldFlags are the writable agent fields shared by create and edit.
type agentFieldFlags struct {
	title, description, instructions, instructionsFile, model string
	tools                                                     []string
}

func (f *agentFieldFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.title, "title", "", "agent title")
	cmd.Flags().StringVar(&f.description, "description", "", "what the agent is for, in a sentence")
	cmd.Flags().StringVar(&f.instructions, "instructions", "", "markdown system prompt")
	cmd.Flags().StringVar(&f.instructionsFile, "instructions-file", "", "read markdown instructions from a file")
	cmd.Flags().StringVar(&f.model, "model", "", "Hub model id (relay/provider/model or @namespace/model)")
	cmd.Flags().StringArrayVar(&f.tools, "tool", nil, "tool group id or tool name (repeatable); none means every tool")
	cmd.MarkFlagsMutuallyExclusive("instructions", "instructions-file")
}

// readInstructions resolves --instructions/--instructions-file; ok is false
// when neither was given.
func (f *agentFieldFlags) readInstructions(cmd *cobra.Command, deps *Deps) (string, bool, error) {
	if cmd.Flags().Changed("instructions") {
		return f.instructions, true, nil
	}
	if !cmd.Flags().Changed("instructions-file") {
		return "", false, nil
	}
	text, err := readFlightInstructions(deps, "", f.instructionsFile)
	return text, err == nil, err
}

func newAgentCreateCmd(deps *Deps) *cobra.Command {
	var f agentFieldFlags
	cmd := &cobra.Command{
		Use:   "create <@namespace/name>",
		Short: "create an agent in a namespace you own or administer",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			instructions, _, err := f.readInstructions(cmd, deps)
			if err != nil {
				return err
			}
			agent, err := deps.Tap.CreateAgent(cmd.Context(), tapper.CreateAgentOptions{
				Ref: args[0], Hub: globalKegTarget(deps).Hub,
				Title: f.title, Description: f.description, Instructions: instructions,
				Model: f.model, Tools: f.tools, Flight: agentFlightFlag(cmd, deps),
			})
			if err != nil {
				return err
			}
			renderAgent(cmd.OutOrStdout(), agent)
			return nil
		},
	}
	f.register(cmd)
	return cmd
}

func newAgentEditCmd(deps *Deps) *cobra.Command {
	var f agentFieldFlags
	var clearTools bool
	cmd := &cobra.Command{
		Use:               "edit <@namespace/name>",
		ValidArgsFunction: agentRefCompletion(deps),
		Short:             "change an agent; unset flags keep their current values",
		Long: `Change an agent in a namespace you own or administer. Only the flags you
pass change; --tool replaces the whole tool list and --all-tools clears it
(every tool). --flight @ns/+slug sets the agent's memory flight and
--flight "" clears it.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := tapper.EditAgentOptions{Ref: args[0], Hub: globalKegTarget(deps).Hub}
			flags := cmd.Flags()
			if flags.Changed("title") {
				opts.Title = &f.title
			}
			if flags.Changed("description") {
				opts.Description = &f.description
			}
			if flags.Changed("model") {
				opts.Model = &f.model
			}
			if instructions, ok, err := f.readInstructions(cmd, deps); err != nil {
				return err
			} else if ok {
				opts.Instructions = &instructions
			}
			switch {
			case clearTools:
				opts.Tools = &[]string{}
			case flags.Changed("tool"):
				opts.Tools = &f.tools
			}
			if flags.Changed("flight") {
				flight := agentFlightFlag(cmd, deps)
				opts.Flight = &flight
			}
			agent, err := deps.Tap.EditAgent(cmd.Context(), opts)
			if err != nil {
				return err
			}
			renderAgent(cmd.OutOrStdout(), agent)
			return nil
		},
	}
	f.register(cmd)
	cmd.Flags().BoolVar(&clearTools, "all-tools", false, "clear the tool list so the agent may use every tool")
	cmd.MarkFlagsMutuallyExclusive("tool", "all-tools")
	return cmd
}

func newAgentDeleteCmd(deps *Deps) *cobra.Command {
	return &cobra.Command{
		Use:               "delete <@namespace/name>",
		ValidArgsFunction: agentRefCompletion(deps),
		Short:             "delete an agent in a namespace you own or administer",
		Args:              cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := deps.Tap.DeleteAgent(cmd.Context(), tapper.DeleteAgentOptions{Ref: args[0], Hub: globalKegTarget(deps).Hub}); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "deleted %s\n", args[0])
			return nil
		},
	}
}

func renderAgent(out io.Writer, a *tapper.HubAgent) {
	fmt.Fprintf(out, "agent: %s\n", a.Ref)
	if a.Title != "" {
		fmt.Fprintf(out, "title: %s\n", a.Title)
	}
	if a.Description != "" {
		fmt.Fprintf(out, "description: %s\n", a.Description)
	}
	fmt.Fprintf(out, "model: %s\n", a.Model)
	tools := "all"
	if len(a.Tools) > 0 {
		tools = strings.Join(a.Tools, ", ")
	}
	fmt.Fprintf(out, "tools: %s\n", tools)
	flight := "(none; no KEG access)"
	if a.Flight != "" {
		flight = a.Flight
	}
	fmt.Fprintf(out, "flight: %s\n", flight)
	if a.Instructions != "" {
		fmt.Fprintf(out, "\n%s\n", a.Instructions)
	}
}

// agentFlightFlag reads the agent's memory flight from the global --flight
// flag, only when it was passed on this command line, so a flight picked up
// from the environment never lands on an agent by accident.
func agentFlightFlag(cmd *cobra.Command, deps *Deps) string {
	if !cmd.Flags().Changed("flight") {
		return ""
	}
	return strings.TrimSpace(deps.KegTargetOptions.Flight)
}

// agentRefCompletion discovers qualified agent references on the selected Hub.
func agentRefCompletion(deps *Deps) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, prefix string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		tap, err := completionTap(deps)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		rows, err := tap.ListAgents(cmd.Context(), tapper.ListAgentsOptions{Hub: globalKegTarget(deps).Hub})
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var refs []string
		for _, row := range rows {
			if strings.HasPrefix(row.Ref, prefix) {
				refs = append(refs, row.Ref)
			}
		}
		return refs, cobra.ShellCompDirectiveNoFileComp
	}
}
