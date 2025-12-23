package webhook

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

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

func ComputeSelectorHash(selector map[string]string) (string, error) {
	if len(selector) == 0 {
		return "", nil
	}

	// Sort keys for deterministic output
	keys := make([]string, 0, len(selector))
	for k := range selector {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Build sorted map
	sortedSelector := make(map[string]string, len(selector))
	for _, k := range keys {
		sortedSelector[k] = selector[k]
	}

	// Marshal to JSON
	data, err := json.Marshal(sortedSelector)
	if err != nil {
		return "", err
	}

	// Compute SHA256 hash
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}

func createPatch(deployment *appsv1.Deployment, img SidecarImage, upstreamDNSAddress string) ([]byte, error) {
	var patches []patchOperation
	var envVars []corev1.EnvVar
	labels := deployment.ObjectMeta.Labels
	hash, err := ComputeSelectorHash(labels)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate hash: %v", err)
	}

	env := corev1.EnvVar{
		Name:  "DNS_MESH_CONFIG_HASH",
		Value: hash,
	}

	envVars = append(envVars, env)
	sidecarContainer := corev1.Container{
		Name:      sidecarName,
		Image:     fmt.Sprintf("%s:%s", img.Name, img.Tag),
		Resources: corev1.ResourceRequirements{},
		Args: []string{"-upstream", fmt.Sprintf("%s:%s", upstreamDNSAddress, "53"), "-controller",
			"http://dns-mesh-controller-controller-manager-metrics-service:9442"},
		Env: envVars,
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

	patches = append(patches, patchOperation{
		Op:    "add",
		Path:  "/spec/template/spec/dnsPolicy",
		Value: corev1.DNSNone,
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
