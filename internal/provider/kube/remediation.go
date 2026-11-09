package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gkdis6/opsgate/internal/action"
	"github.com/gkdis6/opsgate/internal/tool"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type remediationInput struct {
	Cluster   string `json:"cluster,omitempty" jsonschema:"Cluster name; required when several clusters are configured"`
	Namespace string `json:"namespace" jsonschema:"Required allowed namespace"`
	Name      string `json:"name" jsonschema:"Required Pod name"`
	Workload  string `json:"workload,omitempty" jsonschema:"Optional deployment/name, statefulset/name or daemonset/name hint, used only if the Pod is gone"`
}

func (in remediationInput) clusterName() string { return in.Cluster }

type restoreImageInput struct {
	tool.Approval
	Cluster       string `json:"cluster,omitempty" jsonschema:"Cluster name; required when several clusters are configured"`
	Namespace     string `json:"namespace" jsonschema:"Required allowed namespace"`
	Name          string `json:"name" jsonschema:"Required Pod or Deployment name as described by the tool"`
	Container     string `json:"container" jsonschema:"Required regular container name"`
	ExpectedImage string `json:"expected_image" jsonschema:"Exact currently configured failing image; changed values refuse the action"`
	Image         string `json:"image" jsonschema:"Exact image reported by a ready running container in a ready Pod owned by a non-deleting previous ReplicaSet"`
	Revert        bool   `json:"revert,omitempty" jsonschema:"Undo a restore: accept an image from this Deployment's ReplicaSet history even if no ready Pod runs it. The image is not verified healthy."`
}

func (in restoreImageInput) clusterName() string { return in.Cluster }

type imageRestoreOption = action.Choice
type restoreSnapshot struct {
	Summary   string
	UID       types.UID
	Version   string
	Index     int
	SourceUID types.UID
}

var imageReference = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@-]*$`)

func (p *provider) healthyImage(ctx context.Context, d *appsv1.Deployment, container, exclude, target string) (*appsv1.ReplicaSet, string, error) {
	return p.healthyImageBefore(ctx, d, container, exclude, target, nil)
}

func (p *provider) healthyImageBefore(ctx context.Context, d *appsv1.Deployment, container, exclude, target string, before *metav1.Time) (*appsv1.ReplicaSet, string, error) {
	list, err := p.client.AppsV1().ReplicaSets(d.Namespace).List(ctx, metav1.ListOptions{Limit: 501})
	if err != nil {
		return nil, "", err
	}
	if len(list.Items) > 500 || list.Continue != "" {
		return nil, "", tool.Denied("too many ReplicaSets to safely select an image")
	}
	pods, err := p.client.CoreV1().Pods(d.Namespace).List(ctx, metav1.ListOptions{Limit: 501})
	if err != nil {
		return nil, "", err
	}
	if len(pods.Items) > 500 || pods.Continue != "" {
		return nil, "", tool.Denied("too many Pods to safely verify an image")
	}
	sort.Slice(list.Items, func(i, j int) bool { return list.Items[j].CreationTimestamp.Before(&list.Items[i].CreationTimestamp) })
	for i := range list.Items {
		rs := &list.Items[i]
		owner := metav1.GetControllerOf(rs)
		if owner == nil || owner.Kind != "Deployment" || owner.UID != d.UID || rs.Status.ReadyReplicas < 1 || rs.UID == "" || rs.DeletionTimestamp != nil || before != nil && !rs.CreationTimestamp.Before(before) {
			continue
		}
		for _, c := range rs.Spec.Template.Spec.Containers {
			if c.Name == container && c.Image != exclude && (target == "" || c.Image == target) && len(c.Image) <= 1024 && imageReference.MatchString(c.Image) && readyOwnedImage(pods.Items, rs.UID, container, c.Image) {
				return rs, c.Image, nil
			}
		}
	}
	return nil, "", tool.Denied("no non-deleting owned ReplicaSet has a ready Pod running that image")
}

func readyOwnedImage(pods []corev1.Pod, ownerUID types.UID, container, image string) bool {
	for _, pod := range pods {
		owner := metav1.GetControllerOf(&pod)
		if owner == nil || owner.Kind != "ReplicaSet" || owner.UID != ownerUID || pod.UID == "" || pod.DeletionTimestamp != nil || pod.Status.Phase != corev1.PodRunning {
			continue
		}
		ready := false
		for _, condition := range pod.Status.Conditions {
			ready = ready || condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue
		}
		if !ready {
			continue
		}
		for _, spec := range pod.Spec.Containers {
			if spec.Name != container || spec.Image != image {
				continue
			}
			for _, status := range pod.Status.ContainerStatuses {
				if status.Name == container && status.Ready && status.State.Running != nil && reportedImageMatches(image, status.Image, status.ImageID) {
					return true
				}
			}
		}
	}
	return false
}

var runtimeImageDigest = regexp.MustCompile(`^(sha256:[a-fA-F0-9]{64}|sha512:[a-fA-F0-9]{128})$`)

// Require the status image reference to match, allowing Docker Hub/default-tag
// spelling differences only. A runtime digest is mandatory; digest-pinned
// templates must match that digest too. Tags remain mutable registry names.
func reportedImageMatches(template, reported, imageID string) bool {
	if canonicalImage(template) != canonicalImage(reported) {
		return false
	}
	if _, rest, ok := strings.Cut(imageID, "://"); ok {
		imageID = rest
	}
	if _, digest, ok := strings.Cut(imageID, "@"); ok {
		imageID = digest
	}
	if !runtimeImageDigest.MatchString(imageID) {
		return false
	}
	if _, digest, pinned := strings.Cut(template, "@"); pinned {
		return strings.EqualFold(digest, imageID)
	}
	return true
}

func canonicalImage(image string) string {
	if image == "" {
		return ""
	}
	first, _, slash := strings.Cut(image, "/")
	if !slash || !strings.ContainsAny(first, ".:") && first != "localhost" {
		image = "docker.io/" + image
	}
	if strings.HasPrefix(image, "index.docker.io/") {
		image = "docker.io/" + strings.TrimPrefix(image, "index.docker.io/")
	}
	if strings.HasPrefix(image, "docker.io/") && !strings.Contains(strings.TrimPrefix(image, "docker.io/"), "/") {
		image = "docker.io/library/" + strings.TrimPrefix(image, "docker.io/")
	}
	if !strings.Contains(image, "@") && strings.LastIndex(image, ":") < strings.LastIndex(image, "/") {
		image += ":latest"
	}
	return image
}

// remediation performs reads only and returns current, policy-bound choices.
func (p *provider) remediation(ctx context.Context, c tool.Caller, in remediationInput) (string, error) {
	out, err := p.remediationOptions(ctx, c, in)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(out)
	return string(raw), err
}

func (p *provider) remediationOptions(ctx context.Context, c tool.Caller, in remediationInput) (action.Options, error) {
	out := action.Options{Actions: []action.Choice{}}
	if err := p.checkNamespace(in.Namespace); err != nil {
		return out, err
	}
	if err := validName(in.Name, false); err != nil {
		return out, err
	}
	if _, err := action.OptionsURL("", p.cfg.Name, in.Namespace, in.Name, in.Workload); err != nil {
		return out, err
	}
	pod, err := p.client.CoreV1().Pods(in.Namespace).Get(ctx, in.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		out.PodGone = true
		if in.Workload == "" {
			return out, nil
		}
		return p.workloadChoices(ctx, c, in, out)
	}
	if err != nil {
		return out, err
	}
	kind, name := "", ""
	owner := metav1.GetControllerOf(pod)
	var d *appsv1.Deployment
	var rs *appsv1.ReplicaSet
	if owner != nil && owner.UID != "" {
		kind, name = strings.ToLower(owner.Kind), owner.Name
		switch owner.Kind {
		case "ReplicaSet":
			rs, err = p.client.AppsV1().ReplicaSets(in.Namespace).Get(ctx, owner.Name, metav1.GetOptions{})
			if err != nil {
				return out, err
			}
			if rs.UID != owner.UID {
				return out, tool.Denied("pod owner changed")
			}
			if dep := metav1.GetControllerOf(rs); dep != nil && dep.Kind == "Deployment" && dep.UID != "" {
				d, err = p.client.AppsV1().Deployments(in.Namespace).Get(ctx, dep.Name, metav1.GetOptions{})
				if err != nil {
					return out, err
				}
				if d.UID != dep.UID {
					return out, tool.Denied("Deployment owner changed")
				}
				kind, name = "deployment", d.Name
				if d.DeletionTimestamp != nil {
					kind = ""
				}
			}
		case "StatefulSet":
			w, e := p.client.AppsV1().StatefulSets(in.Namespace).Get(ctx, name, metav1.GetOptions{})
			if e != nil {
				return out, e
			}
			if w.UID != owner.UID {
				return out, tool.Denied("pod owner changed")
			}
			if w.DeletionTimestamp != nil {
				kind = ""
			}
		case "DaemonSet":
			w, e := p.client.AppsV1().DaemonSets(in.Namespace).Get(ctx, name, metav1.GetOptions{})
			if e != nil {
				return out, e
			}
			if w.UID != owner.UID {
				return out, tool.Denied("pod owner changed")
			}
			if w.DeletionTimestamp != nil {
				kind = ""
			}
		}
	}
	restart, oom, blocked := false, false, false
	if d != nil && d.DeletionTimestamp == nil {
		restart = p.deploymentChoices(ctx, c, &out, d, rs, pod)
	}
	for _, s := range append(append([]corev1.ContainerStatus{}, pod.Status.InitContainerStatuses...), pod.Status.ContainerStatuses...) {
		if s.State.Waiting != nil {
			reason := s.State.Waiting.Reason
			out.Hint = waitingHint(reason)
			if reason != "CrashLoopBackOff" {
				blocked = true
			}
		}
		if s.RestartCount < repeatedRestarts {
			continue
		}
		reason := ""
		if t := s.LastTerminationState.Terminated; t != nil {
			reason = t.Reason
		}
		if t := s.State.Terminated; t != nil {
			reason = t.Reason
		}
		hint, ok := restartAdvice(reason, kind)
		out.Hint = hint
		restart = restart || ok
		oom = oom || reason == "OOMKilled"
	}
	if !blocked && (kind == "deployment" || kind == "statefulset" || kind == "daemonset") && pod.DeletionTimestamp == nil && (restart || oom) {
		reason, ko := "Repeated failures; restarting may clear stale state.", "반복 실패: 재시작으로 오래된 상태를 해소할 수 있습니다."
		if oom {
			reason, ko = "Repeated OOM: restart only buys time; check the memory limit.", "반복 OOM: 재시작은 시간을 벌 뿐입니다. 메모리 제한을 확인하세요."
		}
		p.addChoice(ctx, c, &out, "k8s_rollout_restart", map[string]any{"cluster": p.cfg.Name, "kind": kind, "namespace": in.Namespace, "name": name}, p.cfg.RolloutRestart, reason, ko)
	}
	unhealthy := pod.Status.Phase == corev1.PodUnknown
	if pod.Spec.NodeName != "" {
		node, e := p.client.CoreV1().Nodes().Get(ctx, pod.Spec.NodeName, metav1.GetOptions{})
		if e == nil {
			unhealthy = unhealthy || nodeCondition(node, corev1.NodeReady).Status != corev1.ConditionTrue
		}
	}
	stuck := pod.DeletionTimestamp != nil && time.Since(pod.DeletionTimestamp.Time) > 5*time.Minute
	if owner != nil && owner.UID != "" && (stuck || unhealthy) {
		p.addChoice(ctx, c, &out, "k8s_delete_pod", map[string]any{"cluster": p.cfg.Name, "namespace": in.Namespace, "name": in.Name}, p.cfg.DeletePod, "Stuck or unreachable pod; its controller can replace it.", "멈췄거나 연결할 수 없는 파드입니다. 컨트롤러가 대체할 수 있습니다.")
	}
	if out.Hint == "" {
		if u := unschedulable(pod); u != nil {
			out.Hint = hintUnschedulable
		} else {
			out.Hint = failedHint(pod.Status.Reason, kind)
		}
	}
	return out, nil
}

// workloadChoices treats the link hint as a separate live target, never proof
// of the vanished Pod's ownership. Only UID-verified current pods supply rules.
func (p *provider) workloadChoices(ctx context.Context, c tool.Caller, in remediationInput, out action.Options) (action.Options, error) {
	kind, name, _ := strings.Cut(in.Workload, "/")
	// Controllers name their pods after themselves, so a hint for an unrelated
	// workload cannot reuse this pod's link.
	if !strings.HasPrefix(in.Name, name+"-") {
		return out, nil
	}
	var uid types.UID
	switch kind {
	case "deployment":
		d, err := p.client.AppsV1().Deployments(in.Namespace).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		if d.DeletionTimestamp != nil {
			return out, nil
		}
		uid = d.UID
		if uid != "" {
			p.deploymentChoices(ctx, c, &out, d, nil, nil)
		}
	case "statefulset":
		w, err := p.client.AppsV1().StatefulSets(in.Namespace).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		if w.DeletionTimestamp != nil {
			return out, nil
		}
		uid = w.UID
	case "daemonset":
		w, err := p.client.AppsV1().DaemonSets(in.Namespace).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		if w.DeletionTimestamp != nil {
			return out, nil
		}
		uid = w.UID
	}
	if uid == "" {
		return out, nil
	}
	pods, err := p.client.CoreV1().Pods(in.Namespace).List(ctx, metav1.ListOptions{Limit: 501})
	if err != nil {
		return out, err
	}
	if len(pods.Items) > 500 || pods.Continue != "" {
		return out, tool.Denied("too many Pods to safely evaluate workload choices")
	}
	for _, pod := range pods.Items {
		owner := metav1.GetControllerOf(&pod)
		if owner == nil {
			continue
		}
		owned := strings.ToLower(owner.Kind) == kind && owner.Name == name && owner.UID == uid
		if kind == "deployment" && owner.Kind == "ReplicaSet" {
			rs, err := p.client.AppsV1().ReplicaSets(in.Namespace).Get(ctx, owner.Name, metav1.GetOptions{})
			if err != nil || rs.UID != owner.UID {
				continue
			}
			parent := metav1.GetControllerOf(rs)
			owned = parent != nil && parent.Kind == "Deployment" && parent.Name == name && parent.UID == uid
		}
		if !owned {
			continue
		}
		choices, err := p.remediationOptions(ctx, c, remediationInput{Namespace: in.Namespace, Name: pod.Name})
		if err != nil {
			continue
		}
		for _, choice := range choices.Actions {
			if choice.Tool == "k8s_delete_pod" {
				continue
			}
			found, images := false, 0
			for _, old := range out.Actions {
				found = found || old.URL == choice.URL
				if old.Tool == "k8s_restore_image" {
					images++
				}
			}
			if choice.Tool == "k8s_restore_image" && images >= 5 {
				continue
			}
			if !found && len(out.Actions) < 7 {
				out.Actions = append(out.Actions, choice)
			}
		}
		if out.Hint == "" {
			out.Hint = choices.Hint
		}
	}
	// Prefer restoration across pods as well as within each pod's evaluation.
	sort.SliceStable(out.Actions, func(i, j int) bool {
		return out.Actions[i].Tool == "k8s_restore_image" && out.Actions[j].Tool != "k8s_restore_image"
	})
	return out, nil
}

func (p *provider) addChoice(ctx context.Context, c tool.Caller, out *action.Options, name string, args map[string]any, patterns []string, reason, ko string) *action.Choice {
	ns, _ := args["namespace"].(string)
	target, _ := args["name"].(string)
	if p.cfg.ActionApprovalURL == "" || p.writeAllowed(ns, target, patterns) != nil || len(out.Actions) >= 7 {
		return nil
	}
	link, err := action.URL(p.cfg.ActionApprovalURL, name, args, "rule")
	if err != nil {
		return nil
	}
	if p.direct != nil {
		direct := p.direct(name)
		if direct == nil {
			return nil
		}
		raw, _ := json.Marshal(args)
		if _, _, err := direct.ValidateLink(ctx, c, raw); err != nil {
			return nil
		}
	}
	entry, _ := action.Lookup(name)
	out.Actions = append(out.Actions, action.Choice{Tool: name, Label: entry.Label("en"), LabelKO: entry.Label("ko"), URL: link, Reason: reason, ReasonKO: ko, Source: "rule"})
	return &out.Actions[len(out.Actions)-1]
}

func (p *provider) deploymentChoices(ctx context.Context, c tool.Caller, out *action.Options, d *appsv1.Deployment, rs *appsv1.ReplicaSet, pod *corev1.Pod) bool {
	restart := false
	if p.writeAllowed(d.Namespace, d.Name, p.cfg.RestoreImage) != nil && p.writeAllowed(d.Namespace, d.Name, p.cfg.RolloutRestart) != nil {
		return false
	}
	currentRS := rs != nil && rs.DeletionTimestamp == nil && equality.Semantic.DeepEqual(rs.Spec.Template.Spec, d.Spec.Template.Spec)
	stalled := workloadOf(d).stalled
	if stalled {
		out.Hint = hintStalled
	}
	for _, container := range d.Spec.Template.Spec.Containers {
		if len(out.Actions) >= 5 {
			break
		}
		imagePull, crash, current := false, false, false
		if pod != nil {
			for _, pc := range pod.Spec.Containers {
				current = current || pc.Name == container.Name && pc.Image == container.Image
			}
			for _, status := range pod.Status.ContainerStatuses {
				if status.Name != container.Name {
					continue
				}
				if w := status.State.Waiting; w != nil {
					imagePull = w.Reason == "ImagePullBackOff" || w.Reason == "ErrImagePull" || w.Reason == "InvalidImageName"
					crash = w.Reason == "CrashLoopBackOff"
				}
				crash = crash || status.RestartCount >= repeatedRestarts
			}
			if !current && !stalled {
				continue
			} // A stale Pod is not rollout evidence.
		}
		if pod == nil && !stalled {
			continue
		}
		// Image pull keeps its historical candidate selection. Crash/stalled
		// rollout choices prefer a source older than the current ReplicaSet.
		var before *metav1.Time
		if !imagePull && !stalled && currentRS {
			before = &rs.CreationTimestamp
		}
		good, image, err := p.healthyImageBefore(ctx, d, container.Name, container.Image, "", before)
		if err != nil {
			continue
		}
		newer := currentRS && good.CreationTimestamp.Before(&rs.CreationTimestamp)
		restart = restart || crash && newer
		unready := pod != nil && !podReady(pod) && newer
		if !imagePull && !(crash && newer) && !stalled && !unready {
			continue
		}
		reason, ko := "Image pull failed; a ready image is available.", "이미지 가져오기 실패: 준비된 이미지가 있습니다."
		if !imagePull {
			reason, ko = "New rollout is failing; a previous image is ready.", "새 배포가 실패하고 있습니다. 이전 이미지가 준비되어 있습니다."
		}
		option := p.addChoice(ctx, c, out, "k8s_restore_image", map[string]any{"cluster": p.cfg.Name, "namespace": d.Namespace, "name": d.Name, "container": container.Name, "expected_image": container.Image, "image": image}, p.cfg.RestoreImage, reason, ko)
		if option != nil {
			option.Container, option.CurrentImage, option.Image, option.SourceReplicaSet = container.Name, container.Image, image, good.Name
		}
	}
	return restart
}

func podReady(pod *corev1.Pod) bool {
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
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
	undo := &tool.Undo{Tool: "k8s_restore_image", Args: map[string]any{"cluster": p.cfg.Name, "namespace": in.Namespace, "name": in.Name, "container": in.Container, "expected_image": in.Image, "image": in.ExpectedImage}}
	if in.Revert {
		source, err := p.historyImage(ctx, d, in.Container, in.Image)
		if err != nil {
			return tool.Plan{}, err
		}
		// Undoing a revert is an ordinary restore, which needs a ready source.
		summary := fmt.Sprintf("Return container image of deployment %s/%s (UID %q), container %q: %q -> %q. Source: non-deleting ReplicaSet %q (UID %q) in this Deployment's history. This image is NOT verified healthy: no ready Pod may be running it. Only this container image changes; configuration is not rolled back.", in.Namespace, in.Name, d.UID, in.Container, in.ExpectedImage, in.Image, source.Name, source.UID)
		return tool.Plan{Summary: summary, NoWindow: true, Undo: undo, Check: p.workloadCheck("deployment", in.Namespace, in.Name), Data: restoreSnapshot{Summary: summary, UID: d.UID, Version: d.ResourceVersion, Index: index, SourceUID: source.UID}}, nil
	}
	source, image, err := p.healthyImage(ctx, d, in.Container, in.ExpectedImage, in.Image)
	if err != nil {
		return tool.Plan{}, err
	}
	undo.Args["revert"] = true
	summary := fmt.Sprintf("Restore container image of deployment %s/%s (UID %q), container %q: %q -> %q. Source: non-deleting ReplicaSet %q (UID %q) has a ready owned Pod with this container ready and running the reported image. Readiness does not guarantee application health or immutable tags. Only this container image changes; configuration is not rolled back.", in.Namespace, in.Name, d.UID, in.Container, in.ExpectedImage, image, source.Name, source.UID)
	return tool.Plan{Summary: summary, NoWindow: true, Undo: undo, Check: p.workloadCheck("deployment", in.Namespace, in.Name), Data: restoreSnapshot{Summary: summary, UID: d.UID, Version: d.ResourceVersion, Index: index, SourceUID: source.UID}}, nil
}

// historyImage finds a non-deleting ReplicaSet owned by d whose pod template
// runs target in container. Unlike healthyImage it needs no ready Pod, so it is
// only used to undo a restore, which a person approves knowing that.
func (p *provider) historyImage(ctx context.Context, d *appsv1.Deployment, container, target string) (*appsv1.ReplicaSet, error) {
	list, err := p.client.AppsV1().ReplicaSets(d.Namespace).List(ctx, metav1.ListOptions{Limit: 501})
	if err != nil {
		return nil, err
	}
	if len(list.Items) > 500 || list.Continue != "" {
		return nil, tool.Denied("too many ReplicaSets to safely select an image")
	}
	sort.Slice(list.Items, func(i, j int) bool { return list.Items[j].CreationTimestamp.Before(&list.Items[i].CreationTimestamp) })
	for i := range list.Items {
		rs := &list.Items[i]
		owner := metav1.GetControllerOf(rs)
		if owner == nil || owner.Kind != "Deployment" || owner.UID != d.UID || rs.UID == "" || rs.DeletionTimestamp != nil {
			continue
		}
		for _, c := range rs.Spec.Template.Spec.Containers {
			if c.Name == container && c.Image == target {
				return rs, nil
			}
		}
	}
	return nil, tool.Denied("no non-deleting ReplicaSet of this Deployment has used that image")
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
		return "", tool.Denied("workload or ready image source changed; approve again")
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
