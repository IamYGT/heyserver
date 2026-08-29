package nginx

import (
	"strings"
	"testing"
)

func TestBuildTemplateSeparatesHTTPOnlyAndTLSListeners(t *testing.T) {
	t.Parallel()

	httpOnly, err := buildTemplate(CreateRequest{
		Domain:  "static.example",
		Type:    "static",
		DocRoot: "/srv/static.example/httpdocs",
	}, "/etc/nginx/snippets")
	if err != nil {
		t.Fatal(err)
	}
	for _, unexpected := range []string{"listen 443", "ssl_certificate", "hserver-ssl-params.conf"} {
		if strings.Contains(httpOnly, unexpected) {
			t.Fatalf("HTTP-only template contains %q:\n%s", unexpected, httpOnly)
		}
	}
	if !strings.Contains(httpOnly, "listen 80;") || strings.Contains(httpOnly, "return 301 https://") {
		t.Fatalf("HTTP-only template does not serve directly:\n%s", httpOnly)
	}

	tls, err := buildTemplate(CreateRequest{
		Domain:  "static.example",
		Type:    "static",
		DocRoot: "/srv/static.example/httpdocs",
		UseSSL:  true,
	}, "/etc/nginx/snippets")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"listen 443 ssl;", "return 301 https://$host$request_uri", "ssl_certificate", "hserver-ssl-params.conf"} {
		if !strings.Contains(tls, expected) {
			t.Fatalf("TLS template is missing %q:\n%s", expected, tls)
		}
	}
}

func TestBuildRedirectTemplateDoesNotInventTLSForHTTPOnlySite(t *testing.T) {
	t.Parallel()

	content, err := buildTemplate(CreateRequest{
		Domain:     "redirect.example",
		Type:       "redirect",
		RedirectTo: "target.example",
	}, "/etc/nginx/snippets")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(content, "server {") != 1 || strings.Contains(content, "listen 443") || strings.Contains(content, "ssl_certificate") {
		t.Fatalf("HTTP-only redirect template contains a TLS server:\n%s", content)
	}
}
