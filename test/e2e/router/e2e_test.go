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

package router

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientset "github.com/volcano-sh/kthena/client-go/clientset/versioned"
	networkingv1alpha1 "github.com/volcano-sh/kthena/pkg/apis/networking/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
	"sigs.k8s.io/yaml"
)

const (
	testNamespace = "default"
	routerSvc     = "kthena-router"
)

var (
	commonComponentsDeployed = false
)

// setupCommonComponents deploys common components that will be used by all test cases.
// This includes the LLM mock deployments and ModelServers.
func setupCommonComponents(t *testing.T, kubeClient *kubernetes.Clientset, kthenaClient *clientset.Clientset, ctx context.Context) {
	if commonComponentsDeployed {
		t.Log("Common components already deployed, skipping setup")
		return
	}

	// Deploy LLM Mock DS1.5B Deployment
	t.Log("Deploying LLM Mock DS1.5B Deployment...")
	deployment1_5b := loadYAML[appsv1.Deployment](t, "../../../examples/kthena-router/LLM-Mock-ds1.5b.yaml")
	_, err := kubeClient.AppsV1().Deployments(testNamespace).Create(ctx, deployment1_5b, metav1.CreateOptions{})
	require.NoError(t, err, "Failed to create DS1.5B Deployment")

	// Deploy LLM Mock DS7B Deployment
	t.Log("Deploying LLM Mock DS7B Deployment...")
	deployment7b := loadYAML[appsv1.Deployment](t, "../../../examples/kthena-router/LLM-Mock-ds7b.yaml")
	_, err = kubeClient.AppsV1().Deployments(testNamespace).Create(ctx, deployment7b, metav1.CreateOptions{})
	require.NoError(t, err, "Failed to create DS7B Deployment")

	// Wait for deployments to be ready
	t.Log("Waiting for deployments to be ready...")
	require.Eventually(t, func() bool {
		deploy1_5b, err := kubeClient.AppsV1().Deployments(testNamespace).Get(ctx, deployment1_5b.Name, metav1.GetOptions{})
		if err != nil {
			return false
		}
		deploy7b, err := kubeClient.AppsV1().Deployments(testNamespace).Get(ctx, deployment7b.Name, metav1.GetOptions{})
		if err != nil {
			return false
		}
		return deploy1_5b.Status.ReadyReplicas == *deploy1_5b.Spec.Replicas &&
			deploy7b.Status.ReadyReplicas == *deploy7b.Spec.Replicas
	}, 5*time.Minute, 5*time.Second, "Deployments did not become ready")

	// Deploy ModelServer DS1.5B
	t.Log("Deploying ModelServer DS1.5B...")
	modelServer1_5b := loadYAML[networkingv1alpha1.ModelServer](t, "../../../examples/kthena-router/ModelServer-ds1.5b.yaml")
	_, err = kthenaClient.NetworkingV1alpha1().ModelServers(testNamespace).Create(ctx, modelServer1_5b, metav1.CreateOptions{})
	require.NoError(t, err, "Failed to create ModelServer DS1.5B")

	// Deploy ModelServer DS7B
	t.Log("Deploying ModelServer DS7B...")
	modelServer7b := loadYAML[networkingv1alpha1.ModelServer](t, "../../../examples/kthena-router/ModelServer-ds7b.yaml")
	_, err = kthenaClient.NetworkingV1alpha1().ModelServers(testNamespace).Create(ctx, modelServer7b, metav1.CreateOptions{})
	require.NoError(t, err, "Failed to create ModelServer DS7B")

	// Wait a bit for ModelServers to be processed
	time.Sleep(10 * time.Second)

	commonComponentsDeployed = true
	t.Log("Common components deployed successfully")
}

// TestModelRouteSimple tests a simple ModelRoute deployment and access.
func TestModelRouteSimple(t *testing.T) {
	ctx := context.Background()

	// Initialize Kubernetes clients
	config, err := getKubeConfig()
	require.NoError(t, err, "Failed to get kubeconfig")
	kubeClient, err := kubernetes.NewForConfig(config)
	require.NoError(t, err, "Failed to create Kubernetes client")
	kthenaClient, err := clientset.NewForConfig(config)
	require.NoError(t, err, "Failed to create kthena client")

	// Setup common components
	setupCommonComponents(t, kubeClient, kthenaClient, ctx)

	// Deploy ModelRoute
	t.Log("Deploying ModelRoute...")
	modelRoute := loadYAML[networkingv1alpha1.ModelRoute](t, "../../../examples/kthena-router/ModelRouteSimple.yaml")
	createdModelRoute, err := kthenaClient.NetworkingV1alpha1().ModelRoutes(testNamespace).Create(ctx, modelRoute, metav1.CreateOptions{})
	require.NoError(t, err, "Failed to create ModelRoute")
	assert.NotNil(t, createdModelRoute)
	t.Logf("Created ModelRoute: %s/%s", createdModelRoute.Namespace, createdModelRoute.Name)

	// Wait a bit for ModelRoute to be processed
	time.Sleep(10 * time.Second)

	// Test accessing the model route
	executeChatInCluster(t, kubeClient, ctx, config, modelRoute.Spec.ModelName)
}

func executeChatInCluster(t *testing.T, kubeClient *kubernetes.Clientset, ctx context.Context, config *rest.Config, modelName string) {
	// Start a nginx pod and then exec curl command in the pod
	podName, err := runNginxPod(t, kubeClient, ctx)
	require.NoError(t, err, "Failed to run nginx pod")

	var stdout, stderr bytes.Buffer
	req := kubeClient.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(testNamespace).
		SubResource("exec")

	command := []string{
		"sh",
		"-c",
		fmt.Sprintf("curl -s -X POST -H 'Content-Type: application/json' -d '{\"model\": \"%s\", \"messages\": [{\"role\": \"user\", \"content\": \"Hello\"}], \"stream\": false}' http://%s/v1/chat/completions", modelName, routerSvc),
	}

	option := &corev1.PodExecOptions{
		Command: command,
		Stdin:   false,
		Stdout:  true,
		Stderr:  true,
		TTY:     false,
	}
	req.VersionedParams(
		option,
		scheme.ParameterCodec,
	)
	exec, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	require.NoError(t, err, "Failed to create executor")
	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})
	require.NoError(t, err, "Failed to execute command in pod")

	// Log stderr for debugging
	if stderr.String() != "" {
		t.Logf("Command stderr: %s", stderr.String())
	}

	// Check if response contains expected result
	responseStr := stdout.String()
	t.Logf("Chat response: %s", responseStr)

	// Verify response is successful
	assert.NotEmpty(t, responseStr, "Chat response is empty")
	assert.NotContains(t, responseStr, "error", "Chat response contains error")
}

func runNginxPod(t *testing.T, kubeClient *kubernetes.Clientset, ctx context.Context) (string, error) {
	podName := "test-nginx-pod-router"
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: testNamespace,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:    "nginx",
					Image:   "nginx:alpine",
					Command: []string{"sleep", "3600"},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100m"),
							corev1.ResourceMemory: resource.MustParse("100Mi"),
						},
					},
				},
			},
			RestartPolicy: corev1.RestartPolicyNever,
		},
	}

	// Check if pod already exists, delete it first
	_, err := kubeClient.CoreV1().Pods(testNamespace).Get(ctx, podName, metav1.GetOptions{})
	if err == nil {
		kubeClient.CoreV1().Pods(testNamespace).Delete(ctx, podName, metav1.DeleteOptions{})
		time.Sleep(2 * time.Second)
	}

	_, err = kubeClient.CoreV1().Pods(testNamespace).Create(ctx, pod, metav1.CreateOptions{})
	require.NoError(t, err, "Failed to create nginx pod")
	t.Logf("Created nginx pod: %s/%s", testNamespace, podName)

	require.Eventually(t, func() bool {
		pod, err := kubeClient.CoreV1().Pods(testNamespace).Get(ctx, podName, metav1.GetOptions{})
		if err != nil {
			t.Logf("Get pod error: %v", err)
			return false
		}
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
				return true
			}
		}
		return false
	}, 5*time.Minute, 5*time.Second, "Pod did not become ready")

	return podName, err
}

func getKubeConfig() (*rest.Config, error) {
	// Try in-cluster config first
	config, err := rest.InClusterConfig()
	if err == nil {
		return config, nil
	}

	// Fall back to kubeconfig
	return clientcmd.BuildConfigFromFlags("", clientcmd.RecommendedHomeFile)
}

// loadYAML loads a YAML file and unmarshals it into the specified type.
func loadYAML[T any](t *testing.T, path string) *T {
	// Get the absolute path relative to the test file location
	absPath, err := filepath.Abs(path)
	if err != nil {
		// Fallback to relative path
		absPath = path
	}

	data, err := os.ReadFile(absPath)
	require.NoError(t, err, fmt.Sprintf("Failed to read YAML file: %s", absPath))

	var obj T
	err = yaml.Unmarshal(data, &obj)
	require.NoError(t, err, fmt.Sprintf("Failed to unmarshal YAML file: %s", absPath))

	return &obj
}
