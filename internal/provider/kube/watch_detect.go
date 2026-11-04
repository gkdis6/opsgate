package kube

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/gkdis6/opsgate/internal/alert"
	"github.com/gkdis6/opsgate/internal/redact"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// slimObject keeps only the fields the detectors compare, so the watch
// cache holds no pod specs, environment or ConfigMap values. It must be
// idempotent (see cache.TransformFunc).
func slimObject(obj any) (any, error) {
	switch o := obj.(type) {
	case *corev1.Pod:
		return &corev1.Pod{ObjectMeta: slimMeta(o.ObjectMeta), Status: corev1.PodStatus{
			Phase:                 o.Status.Phase,
			Reason:                o.Status.Reason,
			Message:               o.Status.Message,
			Conditions:            slimConditions(o.Status.Conditions, corev1.PodScheduled),
			InitContainerStatuses: slimStatuses(o.Status.InitContainerStatuses),
			ContainerStatuses:     slimStatuses(o.Status.ContainerStatuses),
		}}, nil
	case *appsv1.Deployment:
		d := &appsv1.Deployment{ObjectMeta: slimMeta(o.ObjectMeta)}
		d.Spec.Replicas = o.Spec.Replicas
		d.Spec.Template.Spec = slimPodSpec(o.Spec.Template.Spec)
		for _, c := range o.Status.Conditions {
			if c.Type == appsv1.DeploymentProgressing {
				d.Status.Conditions = append(d.Status.Conditions, appsv1.DeploymentCondition{Type: c.Type, Status: c.Status, Reason: c.Reason})
			}
		}
		return d, nil
	case *appsv1.StatefulSet:
		s := &appsv1.StatefulSet{ObjectMeta: slimMeta(o.ObjectMeta)}
		s.Spec.Replicas = o.Spec.Replicas
		s.Spec.Template.Spec = slimPodSpec(o.Spec.Template.Spec)
		return s, nil
	case *appsv1.DaemonSet:
		d := &appsv1.DaemonSet{ObjectMeta: slimMeta(o.ObjectMeta)}
		d.Spec.Template.Spec = slimPodSpec(o.Spec.Template.Spec)
		return d, nil
	case *corev1.Node:
		n := &corev1.Node{ObjectMeta: slimMeta(o.ObjectMeta)}
		n.Spec.Unschedulable = o.Spec.Unschedulable
		for _, c := range o.Status.Conditions {
			n.Status.Conditions = append(n.Status.Conditions, corev1.NodeCondition{Type: c.Type, Status: c.Status, Reason: c.Reason})
		}
		return n, nil
	case *corev1.ConfigMap:
		// Values are replaced by digests kept in annotations; a second pass
		// finds no data and keeps the digests.
		m := &corev1.ConfigMap{ObjectMeta: slimMeta(o.ObjectMeta)}
		for k, v := range o.Annotations {
			if strings.HasPrefix(k, digestPrefix) {
				setAnnotation(&m.ObjectMeta, k, v)
			}
		}
		for k, v := range o.Data {
			setAnnotation(&m.ObjectMeta, digestPrefix+k, digest([]byte(v)))
		}
		for k, v := range o.BinaryData {
			setAnnotation(&m.ObjectMeta, digestPrefix+k, digest(v))
		}
		return m, nil
	}
	return obj, nil
}

const digestPrefix = "opsgate.watch/digest."

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:16])
}

func setAnnotation(m *metav1.ObjectMeta, k, v string) {
	if m.Annotations == nil {
		m.Annotations = map[string]string{}
	}
	m.Annotations[k] = v
}

func slimMeta(m metav1.ObjectMeta) metav1.ObjectMeta {
	slim := metav1.ObjectMeta{Name: m.Name, Namespace: m.Namespace, UID: m.UID, ResourceVersion: m.ResourceVersion, Generation: m.Generation, DeletionTimestamp: m.DeletionTimestamp}
	// The pod's controller and template hash let a hint name its workload.
	if owner := metav1.GetControllerOfNoCopy(&m); owner != nil {
		controller := true
		slim.OwnerReferences = []metav1.OwnerReference{{Kind: owner.Kind, Name: owner.Name, Controller: &controller}}
	}
	if hash := m.Labels[appsv1.DefaultDeploymentUniqueLabelKey]; hash != "" {
		slim.Labels = map[string]string{appsv1.DefaultDeploymentUniqueLabelKey: hash}
	}
	return slim
}

func slimConditions(conditions []corev1.PodCondition, keep corev1.PodConditionType) []corev1.PodCondition {
	var result []corev1.PodCondition
	for _, c := range conditions {
		if c.Type == keep {
			result = append(result, corev1.PodCondition{Type: c.Type, Status: c.Status, Reason: c.Reason, Message: c.Message})
		}
	}
	return result
}

func slimStatuses(statuses []corev1.ContainerStatus) []corev1.ContainerStatus {
	var result []corev1.ContainerStatus
	for _, s := range statuses {
		c := corev1.ContainerStatus{Name: s.Name, RestartCount: s.RestartCount}
		if w := s.State.Waiting; w != nil {
			c.State.Waiting = &corev1.ContainerStateWaiting{Reason: w.Reason, Message: w.Message}
		}
		if t := s.LastTerminationState.Terminated; t != nil {
			c.LastTerminationState.Terminated = &corev1.ContainerStateTerminated{Reason: t.Reason, ExitCode: t.ExitCode}
		}
		result = append(result, c)
	}
	return result
}

func slimPodSpec(spec corev1.PodSpec) corev1.PodSpec {
	slim := func(containers []corev1.Container) []corev1.Container {
		var result []corev1.Container
		for _, c := range containers {
			result = append(result, corev1.Container{Name: c.Name, Image: c.Image})
		}
		return result
	}
	return corev1.PodSpec{InitContainers: slim(spec.InitContainers), Containers: slim(spec.Containers)}
}

// clean bounds and redacts text taken from the cluster.
func clean(s string) string {
	s = redact.String(display(s))
	if utf8.RuneCountInString(s) > 300 {
		s = string([]rune(s)[:300]) + "…"
	}
	return s
}

// waitingProblems are container waiting reasons worth a warning.
// CrashLoopBackOff is left to the restart alert that precedes it.
var waitingProblems = []string{"ImagePullBackOff", "ErrImagePull", "InvalidImageName", "CreateContainerConfigError", "CreateContainerError", "RunContainerError"}

func (w *watcher) pod(oldObj, curObj any) []alert.Alert {
	old, _ := oldObj.(*corev1.Pod)
	cur, _ := curObj.(*corev1.Pod)
	if old == nil || cur == nil {
		return nil
	}
	var result []alert.Alert
	owner, ownerName := podOwner(cur)
	warn := func(key, reason, summary, hint string, restart *alert.Workload) {
		result = append(result, alert.Alert{Severity: alert.Warning, Kind: "Pod", Namespace: cur.Namespace, Name: cur.Name, Reason: reason, Summary: summary, Hint: hint, Restart: restart,
			Key: strings.Join([]string{"Pod", cur.Namespace, cur.Name, key}, "/")})
	}
	before := map[string]corev1.ContainerStatus{}
	for _, s := range append(slices.Clone(old.Status.InitContainerStatuses), old.Status.ContainerStatuses...) {
		before[s.Name] = s
	}
	for _, s := range append(slices.Clone(cur.Status.InitContainerStatuses), cur.Status.ContainerStatuses...) {
		prev, seen := before[s.Name]
		if seen && s.RestartCount > prev.RestartCount {
			reason, summary := "Restarted", fmt.Sprintf("container %s restarted (%d restarts)", s.Name, s.RestartCount)
			if t := s.LastTerminationState.Terminated; t != nil {
				if t.Reason != "" {
					reason = t.Reason
				}
				summary += fmt.Sprintf(": %s, exit code %d", display(t.Reason), t.ExitCode)
			}
			hint, restartable := restartAdvice(reason, owner)
			var restart *alert.Workload
			if restartable && s.RestartCount >= repeatedRestarts {
				restart = w.restartTarget(owner, cur.Namespace, ownerName)
			}
			if restart != nil {
				hint += " If they show stale state rather than a bad release, a restart may clear it."
			}
			warn(s.Name+"/restart", reason, summary, hint, restart)
		}
		if s.State.Waiting != nil && slices.Contains(waitingProblems, s.State.Waiting.Reason) &&
			(!seen || prev.State.Waiting == nil || prev.State.Waiting.Reason != s.State.Waiting.Reason) {
			warn(s.Name+"/"+s.State.Waiting.Reason, s.State.Waiting.Reason, fmt.Sprintf("container %s: %s: %s", s.Name, s.State.Waiting.Reason, s.State.Waiting.Message), waitingHint(s.State.Waiting.Reason), nil)
		}
	}
	// A pod being deleted often ends as Failed; that is not a separate problem.
	if cur.Status.Phase == corev1.PodFailed && old.Status.Phase != corev1.PodFailed && cur.DeletionTimestamp == nil {
		reason := cur.Status.Reason
		if reason == "" {
			reason = "Failed"
		}
		warn("failed", reason, fmt.Sprintf("pod failed: %s: %s", reason, cur.Status.Message), failedHint(reason, owner), nil)
	}
	if c := unschedulable(cur); c != nil && unschedulable(old) == nil {
		warn("unschedulable", "Unschedulable", "pod cannot be scheduled: "+c.Message, hintUnschedulable, nil)
	}
	return result
}

func unschedulable(p *corev1.Pod) *corev1.PodCondition {
	for i, c := range p.Status.Conditions {
		if c.Type == corev1.PodScheduled && c.Status == corev1.ConditionFalse && c.Reason == corev1.PodReasonUnschedulable {
			return &p.Status.Conditions[i]
		}
	}
	return nil
}

// workloadState is what the detectors compare across workload kinds.
type workloadState struct {
	kind, namespace, name string
	replicas              *int32
	images                []corev1.Container
	stalled               bool
}

func workloadOf(obj any) *workloadState {
	switch o := obj.(type) {
	case *appsv1.Deployment:
		stalled := false
		for _, c := range o.Status.Conditions {
			stalled = stalled || c.Type == appsv1.DeploymentProgressing && c.Status == corev1.ConditionFalse && c.Reason == "ProgressDeadlineExceeded"
		}
		return &workloadState{"Deployment", o.Namespace, o.Name, o.Spec.Replicas, containers(o.Spec.Template.Spec), stalled}
	case *appsv1.StatefulSet:
		return &workloadState{"StatefulSet", o.Namespace, o.Name, o.Spec.Replicas, containers(o.Spec.Template.Spec), false}
	case *appsv1.DaemonSet:
		return &workloadState{"DaemonSet", o.Namespace, o.Name, nil, containers(o.Spec.Template.Spec), false}
	}
	return nil
}

func containers(spec corev1.PodSpec) []corev1.Container {
	return append(slices.Clone(spec.InitContainers), spec.Containers...)
}

func (w *watcher) workload(oldObj, curObj any) []alert.Alert {
	old, cur := workloadOf(oldObj), workloadOf(curObj)
	ref := cur
	if ref == nil {
		ref = old
	}
	if ref == nil {
		return nil
	}
	info := func(reason, summary string) alert.Alert {
		return alert.Alert{Severity: alert.Info, Kind: ref.kind, Namespace: ref.namespace, Name: ref.name, Reason: reason, Summary: summary}
	}
	switch {
	case old == nil:
		return []alert.Alert{info("Created", "created; images: "+imageList(cur.images))}
	case cur == nil:
		return []alert.Alert{info("Deleted", "deleted")}
	}
	var result []alert.Alert
	if changes := imageChanges(old.images, cur.images); changes != "" {
		result = append(result, info("ImageChanged", changes))
	}
	if old.replicas != nil && cur.replicas != nil && *old.replicas != *cur.replicas {
		result = append(result, info("Scaled", fmt.Sprintf("replicas %d → %d", *old.replicas, *cur.replicas)))
	}
	if cur.stalled && !old.stalled {
		result = append(result, alert.Alert{Severity: alert.Warning, Kind: ref.kind, Namespace: ref.namespace, Name: ref.name, Reason: "ProgressDeadlineExceeded", Summary: "rollout stalled: progress deadline exceeded", Hint: hintStalled})
	}
	return result
}

func imageList(cs []corev1.Container) string {
	var parts []string
	for _, c := range cs {
		parts = append(parts, c.Name+"="+c.Image)
	}
	return strings.Join(parts, ", ")
}

func imageChanges(old, cur []corev1.Container) string {
	var parts []string
	before := map[string]string{}
	for _, c := range old {
		before[c.Name] = c.Image
	}
	for _, c := range cur {
		prev, ok := before[c.Name]
		switch {
		case !ok:
			parts = append(parts, fmt.Sprintf("+%s=%s", c.Name, c.Image))
		case prev != c.Image:
			parts = append(parts, fmt.Sprintf("%s: %s → %s", c.Name, prev, c.Image))
		}
		delete(before, c.Name)
	}
	for _, c := range old {
		if _, removed := before[c.Name]; removed {
			parts = append(parts, "-"+c.Name)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "images " + strings.Join(parts, ", ")
}

var nodePressure = []corev1.NodeConditionType{corev1.NodeMemoryPressure, corev1.NodeDiskPressure, corev1.NodePIDPressure, corev1.NodeNetworkUnavailable}

func (w *watcher) node(oldObj, curObj any) []alert.Alert {
	old, _ := oldObj.(*corev1.Node)
	cur, _ := curObj.(*corev1.Node)
	alertFor := func(n *corev1.Node, severity, reason, summary string) alert.Alert {
		a := alert.Alert{Severity: severity, Kind: "Node", Name: n.Name, Reason: reason, Summary: summary}
		if severity == alert.Warning {
			a.Hint = hintNode
		}
		return a
	}
	switch {
	case old == nil && cur == nil:
		return nil
	case old == nil:
		return []alert.Alert{alertFor(cur, alert.Info, "Created", "node joined")}
	case cur == nil:
		return []alert.Alert{alertFor(old, alert.Info, "Deleted", "node removed")}
	}
	var result []alert.Alert
	oldReady, curReady := nodeCondition(old, corev1.NodeReady), nodeCondition(cur, corev1.NodeReady)
	if oldReady.Status != curReady.Status {
		summary := fmt.Sprintf("Ready %s → %s", display(string(oldReady.Status)), display(string(curReady.Status)))
		if curReady.Reason != "" {
			summary += ": " + curReady.Reason
		}
		if curReady.Status == corev1.ConditionTrue {
			result = append(result, alertFor(cur, alert.Info, "Ready", summary))
		} else {
			result = append(result, alertFor(cur, alert.Warning, "NotReady", summary))
		}
	}
	for _, t := range nodePressure {
		if nodeCondition(cur, t).Status == corev1.ConditionTrue && nodeCondition(old, t).Status != corev1.ConditionTrue {
			result = append(result, alertFor(cur, alert.Warning, string(t), string(t)+" is True"))
		}
	}
	if old.Spec.Unschedulable != cur.Spec.Unschedulable {
		reason, summary := "Uncordoned", "node schedulable again"
		if cur.Spec.Unschedulable {
			reason, summary = "Cordoned", "node cordoned"
		}
		result = append(result, alertFor(cur, alert.Info, reason, summary))
	}
	return result
}

func nodeCondition(n *corev1.Node, t corev1.NodeConditionType) corev1.NodeCondition {
	for _, c := range n.Status.Conditions {
		if c.Type == t {
			return c
		}
	}
	return corev1.NodeCondition{Type: t, Status: corev1.ConditionUnknown}
}

const maxKeysListed = 20

func (w *watcher) configMap(oldObj, curObj any) []alert.Alert {
	old, _ := oldObj.(*corev1.ConfigMap)
	cur, _ := curObj.(*corev1.ConfigMap)
	ref := cur
	if ref == nil {
		ref = old
	}
	if ref == nil {
		return nil
	}
	info := func(reason, summary string) []alert.Alert {
		return []alert.Alert{{Severity: alert.Info, Kind: "ConfigMap", Namespace: ref.Namespace, Name: ref.Name, Reason: reason, Summary: summary}}
	}
	switch {
	case old == nil:
		return info("Created", "created; keys: "+keyList(slices.Sorted(maps.Keys(digests(cur)))))
	case cur == nil:
		return info("Deleted", "deleted")
	}
	before, after := digests(old), digests(cur)
	var changed, added, removed []string
	for k, v := range after {
		prev, ok := before[k]
		switch {
		case !ok:
			added = append(added, k)
		case prev != v:
			changed = append(changed, k)
		}
	}
	for k := range before {
		if _, ok := after[k]; !ok {
			removed = append(removed, k)
		}
	}
	var parts []string
	for _, group := range []struct {
		label string
		keys  []string
	}{{"changed", changed}, {"added", added}, {"removed", removed}} {
		if len(group.keys) > 0 {
			slices.Sort(group.keys)
			parts = append(parts, group.label+": "+keyList(group.keys))
		}
	}
	if len(parts) == 0 {
		return nil
	}
	return info("DataChanged", "keys "+strings.Join(parts, "; "))
}

func digests(m *corev1.ConfigMap) map[string]string {
	result := map[string]string{}
	for k, v := range m.Annotations {
		if key, ok := strings.CutPrefix(k, digestPrefix); ok {
			result[key] = v
		}
	}
	return result
}

func keyList(keys []string) string {
	if len(keys) == 0 {
		return "-"
	}
	if len(keys) > maxKeysListed {
		return strings.Join(keys[:maxKeysListed], ", ") + fmt.Sprintf(" and %d more", len(keys)-maxKeysListed)
	}
	return strings.Join(keys, ", ")
}
