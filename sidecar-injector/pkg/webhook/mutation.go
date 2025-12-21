package webhook

import (
	"encoding/json"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

const (
	sidecarAnnotation = "sidecar-injector.io/inject"
	sidecarName       = "sidecar"
)

type SidecarImage struct {
	Name string `json:"name"`
	Tag  string `json:"tag"`
}

type patchOperation struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value,omitempty"`
}

func shouldInject(deployment *appsv1.Deployment) bool {
	annotations := deployment.Annotations
	if annotations == nil {
		return false
	}

	value, exists := annotations[sidecarAnnotation]
	return exists && value == "true"
}

func createPatch(deployment *appsv1.Deployment, img SidecarImage) ([]byte, error) {
	var patches []patchOperation

	// Create sidecar container
	sidecarContainer := corev1.Container{
		Name:      sidecarName,
		Image:     fmt.Sprintf("%s:%s", img.Name, img.Tag),
		Resources: corev1.ResourceRequirements{},
	}

	// Check if containers array exists
	containers := deployment.Spec.Template.Spec.Containers
	if len(containers) == 0 {
		// If no containers exist, create the array with the sidecar
		patches = append(patches, patchOperation{
			Op:    "add",
			Path:  "/spec/template/spec/containers",
			Value: []corev1.Container{sidecarContainer},
		})
	} else {
		// Add sidecar to existing containers
		patches = append(patches, patchOperation{
			Op:    "add",
			Path:  "/spec/template/spec/containers/-",
			Value: sidecarContainer,
		})
	}

	// Add DNS configuration
	dnsConfig := &corev1.PodDNSConfig{
		Nameservers: []string{"127.0.0.1"},
		Searches: []string{
			"default.svc.cluster.local",
			"svc.cluster.local",
			"cluster.local",
		},
	}
	patches = append(patches, patchOperation{
		Op:    "add",
		Path:  "/spec/template/spec/dnsConfig",
		Value: dnsConfig,
	})

	// Add annotation to mark injection as completed
	if deployment.Spec.Template.Annotations == nil {
		patches = append(patches, patchOperation{
			Op:   "add",
			Path: "/spec/template/metadata/annotations",
			Value: map[string]string{
				"sidecar-injector.io/status": "injected",
			},
		})
	} else {
		patches = append(patches, patchOperation{
			Op:    "add",
			Path:  "/spec/template/metadata/annotations/sidecar-injector.io~1status",
			Value: "injected",
		})
	}

	patchBytes, err := json.Marshal(patches)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal patches: %v", err)
	}

	return patchBytes, nil
}
