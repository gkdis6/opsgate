package kube

import (
	"github.com/gkdis6/opsgate/internal/tool"
	"k8s.io/apimachinery/pkg/types"
)

// writeIdentity is private to one Plan/Apply invocation.
type writeIdentity struct {
	uid            types.UID
	version        string
	hasAnnotations bool
}

func identityPlan(uid types.UID, version, summary string, annotations bool) (tool.Plan, error) {
	if uid == "" || version == "" {
		return tool.Plan{}, tool.Denied("resource identity or version unavailable")
	}
	return tool.Plan{Summary: summary, Data: writeIdentity{uid: uid, version: version, hasAnnotations: annotations}}, nil
}

func plannedIdentity(plan tool.Plan) (writeIdentity, error) {
	identity, ok := plan.Data.(writeIdentity)
	if !ok || identity.uid == "" || identity.version == "" {
		return writeIdentity{}, tool.Denied("write plan identity unavailable")
	}
	return identity, nil
}
