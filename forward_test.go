package k8sport

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// NOTE: This test requires a running Kubernetes cluster and configured kubeconfig.
// It also assumes the Forwarder struct has fields like kc (*rest.RESTClient),
// upgrader (spdy.Upgrader), transport (http.RoundTripper), and reqID (atomic.Uint64)
// based on their usage in the Forward method. You might need to adjust the Forwarder
// instantiation based on its actual definition.

func TestPortForwardHttpbin(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()

	// 1. Load Kubeconfig
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	configOverrides := &clientcmd.ConfigOverrides{}
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)

	config, err := kubeConfig.ClientConfig()
	if err != nil {
		t.Fatalf("Failed to load kubeconfig: %v", err)
	}

	// 2. Create Kubernetes Clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		t.Fatalf("Failed to create clientset: %v", err)
	}

	// 3. Create Test Namespace
	nsName := fmt.Sprintf("portforward-test-%d", rand.Intn(100000))
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: nsName,
		},
	}
	_, err = clientset.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create namespace %s: %v", nsName, err)
	}
	t.Cleanup(func() {
		// Cleanup namespace
		bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err = clientset.CoreV1().Namespaces().Delete(bgCtx, nsName, metav1.DeleteOptions{})
		if err != nil {
			t.Errorf("Cleanup: failed to delete namespace %s: %v", nsName, err)
		}
		t.Logf("Deleted namespace %s", nsName)
	})

	t.Logf("Created namespace %s", nsName)

	// 4. Create httpbin Pod
	podName := "httpbin-test"
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: nsName,
			Labels:    map[string]string{"app": "httpbin"},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "httpbin",
					Image: "kennethreitz/httpbin", // A simple HTTP server image
					Ports: []corev1.ContainerPort{
						{
							ContainerPort: 80,
							Protocol:      corev1.ProtocolTCP,
						},
					},
				},
			},
		},
	}
	httpBinPod, err := clientset.CoreV1().Pods(nsName).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create pod %s/%s: %v", nsName, podName, err)
	}
	t.Logf("Created pod %s/%s", nsName, podName)

	// 5. Wait for Pod Ready
	t.Logf("Waiting for pod %s/%s to be ready...", nsName, podName)
	if err := waitForPodReady(ctx, clientset, nsName, podName); err != nil {
		t.Fatalf("Pod %s/%s did not become ready: %v", nsName, podName, err)
	}

	// 6. Create Forwarder instance
	fw, err := NewForwarder(config)
	if err != nil {
		t.Fatalf("Failed to create Forwarder: %v", err)
	}

	// --- Port Forward ---
	targetPort := "80" // The container port of httpbin
	conn, err := fw.Forward(ctx, *httpBinPod, targetPort)
	if err != nil {
		t.Fatalf("Port forwarding failed: %v", err)
	}
	defer conn.Close()
	t.Logf("Port forward connection established to %s:%s", httpBinPod.Name, targetPort)

	// --- HTTP Requests & Validation ---
	client := conn.HTTPClient()

	testCases := []struct {
		name            string
		path            string
		method          string
		headers         http.Header
		expectedCode    int
		validateBody    func(body map[string]any) error
		validateHeaders func(headers http.Header) error
	}{
		{
			name:         "GET /get",
			path:         "/get",
			method:       http.MethodGet,
			expectedCode: http.StatusOK,
			validateBody: func(body map[string]any) error {
				headersMap, ok := body["headers"].(map[string]any)
				if !ok {
					return fmt.Errorf("missing or invalid 'headers' field in response")
				}
				if _, ok := headersMap["User-Agent"]; !ok {
					return fmt.Errorf("missing 'User-Agent' in response headers")
				}
				// httpbin uses the Host header in the URL field
				urlVal, ok := body["url"].(string)
				if !ok {
					return fmt.Errorf("missing or invalid 'url' field in response")
				}
				if !strings.HasSuffix(urlVal, "/get") {
					return fmt.Errorf("unexpected url value: %s", urlVal)
				}
				return nil
			},
		},
		{
			name:         "GET /headers",
			path:         "/headers",
			method:       http.MethodGet,
			expectedCode: http.StatusOK,
			headers:      http.Header{"X-Test-Header": []string{"k8s-portforward-conn"}},
			validateBody: func(body map[string]any) error {
				headersMap, ok := body["headers"].(map[string]any)
				if !ok {
					return fmt.Errorf("missing or invalid 'headers' field in response")
				}
				host, ok := headersMap["Host"].(string)
				if !ok || host == "" {
					return fmt.Errorf("missing or empty 'Host' header in response")
				}
				test, ok := headersMap["X-Test-Header"].(string)
				if !ok || test != "k8s-portforward-conn" {
					return fmt.Errorf("missing or incorrect 'X-Test-Header' in response")
				}
				return nil
			},
		},
		{
			name:         "GET /ip",
			path:         "/ip",
			method:       http.MethodGet,
			expectedCode: http.StatusOK,
			validateBody: func(body map[string]any) error {
				origin, ok := body["origin"].(string)
				if !ok || origin == "" {
					return fmt.Errorf("missing or empty 'origin' field in response")
				}
				// Origin should be an IP address (likely the connection source within the pod network)
				return nil
			},
		},
	}

	// Use a non-standard host to ensure it's correctly passed
	targetURL := &url.URL{
		Scheme: "http",
		Host:   "kubernetes-internal-test-host", // This is arbitrary, httpbin reflects it
	}

	for _, tc := range testCases {
		tc := tc // old habits die hard
		t.Run(tc.name, func(t *testing.T) {
			// Create request
			reqURL := targetURL.JoinPath(tc.path)
			req, err := http.NewRequestWithContext(ctx, tc.method, reqURL.String(), nil)
			if err != nil {
				t.Fatalf("Failed to create request: %v", err)
			}
			// Add a specific header to test propagation
			if tc.headers != nil {
				for key, values := range tc.headers {
					for _, value := range values {
						req.Header.Add(key, value)
					}
				}
			}

			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("HTTP request failed: %v", err)
			}
			defer resp.Body.Close()

			// Validate Status Code
			if resp.StatusCode != tc.expectedCode {
				t.Errorf("Expected status code %d, got %d", tc.expectedCode, resp.StatusCode)
			}

			// Validate Headers (if specified)
			if tc.validateHeaders != nil {
				if err := tc.validateHeaders(resp.Header); err != nil {
					t.Errorf("Header validation failed: %v", err)
				}
			}

			// Validate Body (if specified)
			if tc.validateBody != nil {
				var bodyJson map[string]any
				if err := json.NewDecoder(resp.Body).Decode(&bodyJson); err != nil {
					// Try reading raw body for debugging non-JSON responses
					rawBody, _ := io.ReadAll(resp.Body) // Read remaining body
					t.Fatalf("Failed to decode JSON response body: %v. Raw Body: %s", err, string(rawBody))
				}
				if err := tc.validateBody(bodyJson); err != nil {
					// Log the full body for debugging failed validations
					bodyBytes, _ := json.MarshalIndent(bodyJson, "", "  ")
					t.Errorf("Body validation failed: %v\nResponse Body:\n%s", err, string(bodyBytes))
				}
			}
			t.Logf("Request %s successful and validated.", tc.path)
		})
	}
}

// waitForPodReady polls the pod status until it is Running and Ready.
func waitForPodReady(ctx context.Context, clientset kubernetes.Interface, ns, podName string) error {
	const (
		pollInterval = 2 * time.Second
		maxWait      = 2 * time.Minute
	)
	startTime := time.Now()

	for {
		pod, err := clientset.CoreV1().Pods(ns).Get(ctx, podName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get pod %s/%s: %w", ns, podName, err)
		}

		if pod.Status.Phase == corev1.PodRunning {
			ready := true
			if len(pod.Status.ContainerStatuses) == 0 {
				ready = false // No container statuses yet means not ready
			}
			for _, status := range pod.Status.ContainerStatuses {
				if !status.Ready {
					ready = false
					break
				}
			}
			if ready {
				return nil // Pod is Running and all containers are Ready
			}
		}

		if pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodSucceeded {
			return fmt.Errorf("pod %s/%s entered terminal phase %s unexpectedly", ns, podName, pod.Status.Phase)
		}

		if time.Since(startTime) > maxWait {
			return fmt.Errorf("timeout waiting for pod %s/%s to become ready", ns, podName)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled while waiting for pod %s/%s: %w", ns, podName, ctx.Err())
		case <-time.After(pollInterval):
			// Continue polling
		}
	}
}
