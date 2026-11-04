package kube

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/gkdis6/opsgate/internal/alert"
	"github.com/gkdis6/opsgate/internal/config"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

// Watch follows clusters until ctx ends and passes observed problems and
// changes to emit. It needs list and watch on pods, deployments,
// statefulsets, daemonsets, configmaps and nodes; RBAC failures are logged
// and retried by client-go.
func Watch(ctx context.Context, clusters []config.KubernetesCluster, problemsOnly bool, emit func(alert.Alert)) error {
	clients := map[string]kubernetes.Interface{}
	for _, cluster := range clusters {
		client, err := clusterClient(cluster)
		if err != nil {
			return fmt.Errorf("cluster %q: %w", cluster.Name, err)
		}
		clients[cluster.Name] = client
	}
	return watch(ctx, clusters, clients, problemsOnly, emit)
}

func watch(ctx context.Context, clusters []config.KubernetesCluster, clients map[string]kubernetes.Interface, problemsOnly bool, emit func(alert.Alert)) error {
	var factories []informers.SharedInformerFactory
	for _, cluster := range clusters {
		w := &watcher{cluster: cluster.Name, policy: namespacePolicy{cluster.Namespaces, cluster.DenyNamespaces}, restart: cluster.RolloutRestart, problemsOnly: problemsOnly, emit: emit, now: time.Now}
		if err := w.policy.validate(); err != nil {
			return fmt.Errorf("cluster %q: %w", cluster.Name, err)
		}
		scopes, err := w.scopes()
		if err != nil {
			return fmt.Errorf("cluster %q: %w", cluster.Name, err)
		}
		client := clients[cluster.Name]
		nodes := informers.NewSharedInformerFactoryWithOptions(client, 0, informers.WithTransform(slimObject))
		if err := w.handle("nodes", nodes.Core().V1().Nodes().Informer(), w.node); err != nil {
			return err
		}
		factories = append(factories, nodes)
		for _, scope := range scopes {
			f := informers.NewSharedInformerFactoryWithOptions(client, 0, informers.WithNamespace(scope), informers.WithTransform(slimObject))
			for _, h := range []struct {
				name     string
				informer cache.SharedIndexInformer
				fn       func(old, cur any) []alert.Alert
			}{
				{"pods", f.Core().V1().Pods().Informer(), w.pod},
				{"configmaps", f.Core().V1().ConfigMaps().Informer(), w.configMap},
				{"deployments", f.Apps().V1().Deployments().Informer(), w.workload},
				{"statefulsets", f.Apps().V1().StatefulSets().Informer(), w.workload},
				{"daemonsets", f.Apps().V1().DaemonSets().Informer(), w.workload},
			} {
				if err := w.handle(h.name, h.informer, h.fn); err != nil {
					return err
				}
			}
			factories = append(factories, f)
		}
		log.Printf("watching cluster %q (%s)", cluster.Name, scopeNames(scopes))
	}
	for _, f := range factories {
		f.Start(ctx.Done())
	}
	<-ctx.Done()
	for _, f := range factories {
		f.Shutdown()
	}
	return nil
}

// scopes returns the namespaces to watch. Glob patterns watch the whole
// cluster and filter locally so namespaces created later are covered.
func (w *watcher) scopes() ([]string, error) {
	var result []string
	for _, pattern := range w.policy.allow {
		if strings.ContainsAny(pattern, "*?[") {
			return []string{""}, nil
		}
		if err := validName(pattern, true); err != nil {
			return nil, err
		}
		if w.policy.allowed(pattern) {
			result = append(result, pattern)
		}
	}
	return result, nil
}

func scopeNames(scopes []string) string {
	switch {
	case len(scopes) == 0:
		return "nodes only"
	case scopes[0] == "":
		return "all namespaces, filtered by policy"
	}
	return "namespaces " + strings.Join(scopes, ", ")
}

type watcher struct {
	cluster      string
	policy       namespacePolicy
	restart      []string // rollout_restart patterns; only used to suggest restarts
	problemsOnly bool
	emit         func(alert.Alert)
	now          func() time.Time
}

// handle reports differences between an object's cached and new state.
// Objects present at start-up are the baseline, not changes.
func (w *watcher) handle(name string, informer cache.SharedIndexInformer, fn func(old, cur any) []alert.Alert) error {
	if err := informer.SetWatchErrorHandlerWithContext(func(_ context.Context, _ *cache.Reflector, err error) {
		log.Printf("cluster %q: watch %s: %v", w.cluster, name, err)
	}); err != nil {
		return err
	}
	_, err := informer.AddEventHandler(cache.ResourceEventHandlerDetailedFuncs{
		AddFunc: func(obj any, initial bool) {
			if !initial {
				w.send(fn(nil, obj))
			}
		},
		UpdateFunc: func(old, cur any) { w.send(fn(old, cur)) },
		DeleteFunc: func(obj any) {
			if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
				obj = tombstone.Obj
			}
			w.send(fn(obj, nil))
		},
	})
	return err
}

func (w *watcher) send(alerts []alert.Alert) {
	for _, a := range alerts {
		if w.problemsOnly && a.Severity != alert.Warning {
			continue
		}
		if a.Namespace != "" && !w.policy.allowed(a.Namespace) {
			continue
		}
		a.Time = w.now().UTC()
		a.Cluster = w.cluster
		a.Summary = clean(a.Summary)
		if a.Key == "" {
			a.Key = strings.Join([]string{a.Kind, a.Namespace, a.Name, a.Reason}, "/")
		}
		a.Key = w.cluster + "/" + a.Key
		w.emit(a)
	}
}
