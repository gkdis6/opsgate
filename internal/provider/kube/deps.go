package kube

// Pins dependencies for parallel work; delete once real code imports them.
import (
	_ "k8s.io/api/core/v1"
	_ "k8s.io/apimachinery/pkg/apis/meta/v1"
	_ "k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/kubernetes/fake"
	_ "k8s.io/client-go/rest"
	_ "k8s.io/client-go/tools/clientcmd"
	_ "sigs.k8s.io/yaml"
)
