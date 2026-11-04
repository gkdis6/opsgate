package tool

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/gkdis6/opsgate/internal/audit"
)

// Direct runs one write tool for a person approving it in a browser, with no
// MCP client to retry the call. Plan and Run make the same checks as the MCP
// path. The approval itself is the caller's job.
type Direct struct {
	// Title names the tool for people.
	Title string
	plan  func(ctx context.Context, c Caller, raw json.RawMessage) (Plan, string, error)
	run   func(ctx context.Context, c Caller, raw json.RawMessage, approved string) (string, error)
}

// Plan returns the plan and the summary a person must see before approving.
func (d *Direct) Plan(ctx context.Context, c Caller, raw json.RawMessage) (Plan, string, error) {
	return d.plan(ctx, c, raw)
}

// Run plans again and applies only if the summary still equals approved.
func (d *Direct) Run(ctx context.Context, c Caller, raw json.RawMessage, approved string) (string, error) {
	return d.run(ctx, c, raw, approved)
}

// Direct returns the named direct write, or nil if the tool is disabled or was
// not registered for direct use.
func (env *Env) Direct(name string) *Direct { return env.direct[name] }

func (env *Env) addDirect(name string, d *Direct) {
	if env.direct == nil {
		env.direct = map[string]*Direct{}
	}
	env.direct[name] = d
}

func newDirect[In WriteInput](env *Env, spec WriteSpec, scope string, maxArgs, maxSummary int, h WriteHandler[In]) *Direct {
	prepare := func(ctx context.Context, c Caller, raw json.RawMessage) (In, Plan, string, map[string]any, error) {
		var in In
		args, hash, argsErr := writeArgs(raw, maxArgs)
		if argsErr == nil && json.Unmarshal(raw, &in) != nil {
			argsErr = Denied("write arguments do not match the tool")
		}
		plan, shown, err := planWrite(ctx, env, spec.Name, scope, maxSummary, h, c, in, args, hash, argsErr)
		if err == nil && plan.FullReviewRequired {
			err = Denied("this write needs a full review in the client")
		}
		return in, plan, shown, args, err
	}
	// record audits the outcome and returns the bounded success text.
	record := func(start time.Time, c Caller, args map[string]any, shown, text string, err error) string {
		ev := audit.Event{Time: start, User: c.User, Transport: c.Transport, Tool: spec.Name, Args: args, Plan: shown, Approval: "web",
			Outcome: "ok", DurationMS: time.Since(start).Milliseconds()}
		if err != nil {
			text = err.Error()
			ev.Outcome = "error"
			if errors.Is(err, ErrDenied) {
				ev.Outcome = "denied"
			}
		}
		text = outputText(text, env.MaxOutputBytes)
		if err != nil {
			ev.Error = text
		} else {
			ev.OutputSize = len(text)
		}
		env.Audit.Record(ev)
		return text
	}
	return &Direct{
		Title: spec.Title,
		plan: func(ctx context.Context, c Caller, raw json.RawMessage) (Plan, string, error) {
			start := time.Now()
			_, plan, shown, args, err := prepare(ctx, c, raw)
			if err != nil {
				// A plan that succeeds is audited by its approval and its run.
				record(start, c, args, shown, "", err)
				return Plan{}, "", err
			}
			return plan, shown, nil
		},
		run: func(ctx context.Context, c Caller, raw json.RawMessage, approved string) (string, error) {
			start := time.Now()
			in, plan, shown, args, err := prepare(ctx, c, raw)
			var text string
			if err == nil && shown != approved {
				err = Denied("the workload changed after this page opened; open the link again and review it")
			}
			if err == nil {
				applyCtx, cancel := context.WithTimeout(ctx, env.Timeout)
				defer cancel()
				text, err = h.Apply(applyCtx, c, in, plan)
			}
			text = record(start, c, args, shown, text, err)
			if err != nil {
				return "", err
			}
			return text, nil
		},
	}
}
