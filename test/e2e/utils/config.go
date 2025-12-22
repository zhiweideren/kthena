/*
Copyright The Volcano Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package utils

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/yaml"
)

// GetKubeConfig returns a Kubernetes REST config.
// It tries in-cluster config first, then falls back to kubeconfig file.
func GetKubeConfig() (*rest.Config, error) {
	// Try in-cluster config first
	config, err := rest.InClusterConfig()
	if err == nil {
		return config, nil
	}

	// Fall back to kubeconfig
	return clientcmd.BuildConfigFromFlags("", clientcmd.RecommendedHomeFile)
}

// LoadYAMLFromFile loads a YAML file from a path relative to the project root
// and unmarshals it into the specified type.
func LoadYAMLFromFile[T any](path string) *T {
	_, filename, _, _ := runtime.Caller(0)
	// Current file is test/e2e/utils/config.go, project root is 3 levels up
	projectRoot := filepath.Join(filepath.Dir(filename), "..", "..", "..")
	absPath := filepath.Join(projectRoot, path)

	data, err := os.ReadFile(absPath)
	if err != nil {
		panic(fmt.Sprintf("Failed to read YAML file from project root: %s (abs: %s): %v", path, absPath, err))
	}

	var obj T
	if err := yaml.Unmarshal(data, &obj); err != nil {
		panic(fmt.Sprintf("Failed to unmarshal YAML file: %s: %v", absPath, err))
	}

	return &obj
}

// RandomString generates a random string of length n.
func RandomString(n int) string {
	const letterBytes = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letterBytes[rand.Intn(len(letterBytes))]
	}
	return string(b)
}
