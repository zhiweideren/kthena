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
	"os"
	"path/filepath"
	"runtime"

	"os/exec"
	"strings"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/yaml"
)

// ApplyYAML applies a YAML string using kubectl apply
func ApplyYAML(yamlStr string) error {
	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(yamlStr)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

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

// LoadYAMLFromFile loads a YAML file and unmarshals it into the specified type.
// This function is used in package-level setup and returns errors instead of failing.
func LoadYAMLFromFile[T any](path string) *T {
	// Get the absolute path relative to the current working directory
	// In test environment, working directory is typically the project root
	absPath, err := filepath.Abs(path)
	if err != nil {
		// Fallback to relative path
		absPath = path
	}

	// Try to read the file
	data, err := os.ReadFile(absPath)
	if err != nil {
		// If relative path doesn't work, try from test file location
		// Get the directory of this test file
		_, testFile, _, _ := runtime.Caller(1)
		testDir := filepath.Dir(testFile)
		absPath = filepath.Join(testDir, path)
		data, err = os.ReadFile(absPath)
		if err != nil {
			panic(fmt.Sprintf("Failed to read YAML file: %s (tried %s): %v", path, absPath, err))
		}
	}

	var obj T
	err = yaml.Unmarshal(data, &obj)
	if err != nil {
		panic(fmt.Sprintf("Failed to unmarshal YAML file: %s: %v", absPath, err))
	}

	return &obj
}
