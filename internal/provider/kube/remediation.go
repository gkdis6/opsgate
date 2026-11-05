package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/gkdis6/opsgate/internal/tool"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type remediationInput struct {
	Cluster   string `json:"cluster,omitempty" jsonschema:"Cluster name; required when several clusters are configured"`
	Namespace string `json:"namespace" jsonschema:"Required allowed namespace"`
	Name      string `json:"name" jsonschema:"Required Pod or Deployment name as described by the tool"`
}

func (in remediationInput) clusterName() string { return in.Cluster }

type restoreImageInput struct {
	tool.Approval
	Cluster       string `json:"cluster,omitempty" jsonschema:"Cluster name; required when several clusters are configured"`
	Namespace     string `json:"namespace" jsonschema:"Required allowed namespace"`
	Name          string `json:"name" jsonschema:"Required Pod or Deployment name as described by the tool"`
	Container     string `json:"container" jsonschema:"Required regular container name"`
	ExpectedImage string `json:"expected_image" jsonschema:"Exact currently configured failing image; changed values refuse the action"`
	Image         string `json:"image" jsonschema:"Exact image from a currently ready previous owned ReplicaSet"`
}

func (in restoreImageInput) clusterName() string { return in.Cluster }

type imageRestoreOption struct {
	Label        string `json:"label"`
	URL          string `json:"url"`
	Container    string `json:"container" jsonschema:"Required regular container name"`
	CurrentImage string `json:"current_image"`
	Image        string `json:"image" jsonschema:"Exact image from a currently ready previous owned ReplicaSet"`
	Source       string `json:"source"`
}
type restoreSnapshot struct {
	Summary   string
	UID       types.UID
	Version   string
	Index     int
	SourceUID types.UID
}

var imageReference = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@-]*$`)

func (p *provider) healthyImage(ctx context.Context, d *appsv1.Deployment, container, exclude, target string) (*appsv1.ReplicaSet, string, error) {
	list, err := p.client.AppsV1().ReplicaSets(d.Namespace).List(ctx, metav1.ListOptions{Limit: 501})
	if err != nil {
		return nil, "", err
	}
	if len(list.Items) > 500 || list.Continue != "" {
		return nil, "", tool.Denied("too many ReplicaSets to safely select an image")
	}
	sort.Slice(list.Items, func(i, j int) bool { return list.Items[j].CreationTimestamp.Before(&list.Items[i].CreationTimestamp) })
	for i := range list.Items {
		rs := &list.Items[i]
		owner := metav1.GetControllerOf(rs)
		if owner == nil || owner.Kind != "Deployment" || owner.UID != d.UID || rs.Status.ReadyReplicas < 1 || rs.UID == "" {
			continue
		}
		for _, c := range rs.Spec.Template.Spec.Containers {
			if c.Name == container && c.Image != exclude && (target == "" || c.Image == target) && len(c.Image) <= 1024 && imageReference.MatchString(c.Image) {
				return rs, c.Image, nil
			}
		}
	}
	return nil, "", tool.Denied("no currently ready previous ReplicaSet supplies that image")
}

// remediation performs reads only and returns exact, policy-bound approval links.
func (p *provider) remediation(ctx context.Context, _ tool.Caller, in remediationInput) (string, error) {
	if err := p.checkNamespace(in.Namespace); err != nil {
		return "", err
	}
	if err := validName(in.Name, false); err != nil {
		return "", err
	}
	options := []imageRestoreOption{}
	if len(p.cfg.RestoreImage) == 0 || p.cfg.ActionApprovalURL == "" {
		return `{"actions":[]}`, nil
	}
	pod, err := p.client.CoreV1().Pods(in.Namespace).Get(ctx, in.Name, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	owner := metav1.GetControllerOf(pod)
	if owner == nil || owner.Kind != "ReplicaSet" {
		return `{"actions":[]}`, nil
	}
	rs, err := p.client.AppsV1().ReplicaSets(in.Namespace).Get(ctx, owner.Name, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	if rs.UID != owner.UID {
		return "", tool.Denied("pod owner changed")
	}
	depOwner := metav1.GetControllerOf(rs)
	if depOwner == nil || depOwner.Kind != "Deployment" {
		return `{"actions":[]}`, nil
	}
	if err := p.writeAllowed(in.Namespace, depOwner.Name, p.cfg.RestoreImage); err != nil {
		return `{"actions":[]}`, nil
	}
	d, err := p.client.AppsV1().Deployments(in.Namespace).Get(ctx, depOwner.Name, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	if d.UID != depOwner.UID {
		return "", tool.Denied("Deployment owner changed")
	}
	for _, status := range pod.Status.ContainerStatuses {
		if status.State.Waiting == nil || !strings.Contains("|ImagePullBackOff|ErrImagePull|InvalidImageName|", "|"+status.State.Waiting.Reason+"|") {
			continue
		}
		for _, c := range d.Spec.Template.Spec.Containers {
			if c.Name != status.Name {
				continue
			}
			// A stale failed Pod must not propose changing a newer Deployment image.
			current := false
			for _, pc := range pod.Spec.Containers {
				if pc.Name == c.Name && pc.Image == c.Image {
					current = true
				}
			}
			if !current {
				continue
			}
			good, image, err := p.healthyImage(ctx, d, c.Name, c.Image, "")
			if err != nil {
				continue
			}
			query := url.Values{"container": {c.Name}, "expected_image": {c.Image}, "image": {image}}
			link := strings.TrimRight(p.cfg.ActionApprovalURL, "/") + "/approve/restore-image/" + url.PathEscape(p.cfg.Name) + "/" + url.PathEscape(d.Namespace) + "/" + url.PathEscape(d.Name) + "?" + query.Encode()
			options = append(options, imageRestoreOption{Label: "이미지 복구 승인", URL: link, Container: c.Name, CurrentImage: c.Image, Image: image, Source: good.Name})
			if len(options) >= 5 {
				break
			}
		}
		if len(options) >= 5 {
			break
		}
	}
	raw, err := json.Marshal(map[string]any{"actions": options})
	return string(raw), err
}

func (p *provider) planRestoreImage(ctx context.Context, _ tool.Caller, in restoreImageInput) (tool.Plan, error) {
	if err := p.writeAllowed(in.Namespace, in.Name, p.cfg.RestoreImage); err != nil {
		return tool.Plan{}, err
	}
	if err := validName(in.Container, false); err != nil || len(in.Image) > 1024 || len(in.ExpectedImage) > 1024 || !imageReference.MatchString(in.Image) || !imageReference.MatchString(in.ExpectedImage) || in.Image == in.ExpectedImage {
		return tool.Plan{}, tool.Denied("invalid container or image transition")
	}
	d, err := p.client.AppsV1().Deployments(in.Namespace).Get(ctx, in.Name, metav1.GetOptions{})
	if err != nil {
		return tool.Plan{}, err
	}
	if d.UID == "" {
		return tool.Plan{}, tool.Denied("Deployment identity unavailable")
	}
	index := -1
	for i, c := range d.Spec.Template.Spec.Containers {
		if c.Name == in.Container && c.Image == in.ExpectedImage {
			index = i
		}
	}
	if index < 0 {
		return tool.Plan{}, tool.Denied("Deployment image changed; reopen a fresh action link")
	}
	source, image, err := p.healthyImage(ctx, d, in.Container, in.ExpectedImage, in.Image)
	if err != nil {
		return tool.Plan{}, err
	}
	summary := fmt.Sprintf("Restore container image of deployment %s/%s (UID %q), container %q: %q -> %q. Source: currently ready ReplicaSet %q (UID %q). Only this container image changes; configuration is not rolled back.", in.Namespace, in.Name, d.UID, in.Container, in.ExpectedImage, image, source.Name, source.UID)
	return tool.Plan{Summary: summary, NoWindow: true, Data: restoreSnapshot{Summary: summary, UID: d.UID, Version: d.ResourceVersion, Index: index, SourceUID: source.UID}}, nil
}
func (p *provider) applyRestoreImage(ctx context.Context, c tool.Caller, in restoreImageInput, approved tool.Plan) (string, error) {
	old, ok := approved.Data.(restoreSnapshot)
	if !ok {
		return "", tool.Denied("missing approved image plan")
	}
	fresh, err := p.planRestoreImage(ctx, c, in)
	if err != nil {
		return "", err
	}
	snap := fresh.Data.(restoreSnapshot)
	if fresh.Summary != old.Summary || snap.UID != old.UID || snap.SourceUID != old.SourceUID {
		return "", tool.Denied("workload or healthy image source changed; approve again")
	}
	path := fmt.Sprintf("/spec/template/spec/containers/%d/", snap.Index)
	ops := []map[string]any{{"op": "test", "path": "/metadata/uid", "value": snap.UID}, {"op": "test", "path": "/metadata/resourceVersion", "value": snap.Version}, {"op": "test", "path": path + "name", "value": in.Container}, {"op": "test", "path": path + "image", "value": in.ExpectedImage}, {"op": "replace", "path": path + "image", "value": in.Image}}
	raw, _ := json.Marshal(ops)
	_, err = p.client.AppsV1().Deployments(in.Namespace).Patch(ctx, in.Name, types.JSONPatchType, raw, metav1.PatchOptions{})
	if err != nil {
		return "", err
	}
	return "Restored the approved container image; verify the Deployment rollout and application health.", nil
}
