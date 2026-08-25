package s3

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSTSEndpointRejectsHostInjectionBeforeTokenRead(t *testing.T) {
	for _, region := range []string{"evil.example/", "evil@example", "evil:443", "evil%2fpath", "eu-wést-1"} {
		t.Run(region, func(t *testing.T) {
			t.Setenv(envRegion, region)
			t.Setenv(envSTSLegacy, "regional")
			if _, err := stsEndpoint(); err == nil {
				t.Fatal("stsEndpoint accepted an invalid region")
			}
		})
	}

	t.Setenv(envRegion, "eu-west-2")
	t.Setenv(envSTSLegacy, "regional")
	got, err := stsEndpoint()
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://sts.eu-west-2.amazonaws.com/" {
		t.Fatalf("stsEndpoint = %q", got)
	}
}

func TestAmbientCredentialExchangesRefuseRedirects(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308} {
		for _, service := range []string{"sts", "imds"} {
			t.Run(service+"/"+http.StatusText(status), func(t *testing.T) {
				clearAWSEnv(t)
				var redirected atomic.Int32
				target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
					redirected.Add(1)
				}))
				defer target.Close()
				redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Location", target.URL)
					w.WriteHeader(status)
				}))
				defer redirector.Close()

				switch service {
				case "sts":
					t.Setenv(envRoleARN, "arn:aws:iam::123456789012:role/test")
					t.Setenv(envTokenFile, writeTokenFile(t, "canary-jwt"))
					stsHost = redirector.URL
					t.Cleanup(func() { stsHost = "" })
				case "imds":
					imdsBase = redirector.URL
					t.Cleanup(func() { imdsBase = imdsLiteralBase })
				}
				_, err := AmbientProvider(&http.Client{}, nil).Retrieve(context.Background())
				if err == nil {
					t.Fatal("redirecting credential exchange succeeded")
				}
				if redirected.Load() != 0 {
					t.Fatalf("credential redirect target contacted %d times", redirected.Load())
				}
			})
		}
	}
}

func TestSignedS3RequestsRefuseEveryRedirect(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var redirected atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				redirected.Add(1)
			}))
			defer target.Close()
			redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Location", target.URL)
				w.WriteHeader(status)
			}))
			defer redirector.Close()

			client, err := New(Config{
				Endpoint: redirector.URL, Region: "us-east-1", Bucket: "bucket", PathStyle: true,
				Credentials: StaticProvider{Credentials: Credentials{AccessKeyID: "id", SecretAccessKey: "secret", SessionToken: "session"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.Get(t.Context(), "key"); err == nil {
				t.Fatal("redirecting signed request succeeded")
			}
			if redirected.Load() != 0 {
				t.Fatalf("redirect target contacted %d times", redirected.Load())
			}
		})
	}
}

func TestSTSFailureDoesNotReflectResponseBody(t *testing.T) {
	const canary = "reflected-request-secret"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, canary, http.StatusForbidden)
	}))
	defer srv.Close()

	t.Setenv(envRoleARN, "arn:aws:iam::123456789012:role/test")
	t.Setenv(envTokenFile, writeTokenFile(t, "jwt"))
	stsHost = srv.URL
	t.Cleanup(func() { stsHost = "" })
	_, err := AmbientProvider(&http.Client{}, nil).Retrieve(context.Background())
	if err == nil {
		t.Fatal("STS failure returned nil")
	}
	if strings.Contains(err.Error(), canary) {
		t.Fatalf("error reflected response body: %v", err)
	}
}

func writeTokenFile(t *testing.T, token string) string {
	t.Helper()
	path := t.TempDir() + "/token"
	if err := os.WriteFile(path, []byte(token), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
