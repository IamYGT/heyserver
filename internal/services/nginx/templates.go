package nginx

import (
	"fmt"
	"path/filepath"
	"strings"
)

// buildTemplate dispatches to the correct template builder based on req.Type.

func buildTemplate(req CreateRequest, snippetsDir string) (string, error) {
	var content string
	var err error
	switch req.Type {
	case "php":
		content, err = buildPHPTemplate(req)
	case "proxy":
		content, err = buildProxyTemplate(req)
	case "static":
		content, err = buildStaticTemplate(req)
	case "redirect":
		content, err = buildRedirectTemplate(req)
	default:
		return "", fmt.Errorf("unknown vhost type %q: expected php|proxy|static|redirect", req.Type)
	}
	if err != nil {
		return "", err
	}
	return bindManagedSnippetPaths(content, snippetsDir), nil
}

func bindManagedSnippetPaths(content, snippetsDir string) string {
	content = strings.ReplaceAll(content, "    include snippets/cloudflare-real-ip.conf;\n", "")
	for source, managed := range map[string]string{
		"acme-challenge.conf":   "hserver-acme-challenge.conf",
		"ssl-params.conf":       "hserver-ssl-params.conf",
		"security-headers.conf": "hserver-security-headers.conf",
		"security-deny.conf":    "hserver-security-deny.conf",
		"gzip-brotli.conf":      "hserver-compression.conf",
		"static-cache.conf":     "hserver-static-cache.conf",
		"php-fpm.conf":          "hserver-php-fpm.conf",
		"proxy-params.conf":     "hserver-proxy-params.conf",
	} {
		content = strings.ReplaceAll(content, "snippets/"+source, filepath.Join(snippetsDir, managed))
	}
	return content
}

// ---------- PHP-FPM template ----------

// buildPHPTemplate generates a Laravel/PHP-FPM vhost config.
//
// Rules enforced:
//   - NEVER listen on a specific IP; bind both IPv4 and IPv6 wildcard listeners.
//   - Emit HTTP-only listeners unless TLS was explicitly requested.
//   - Includes: security-headers.conf and the installation-owned managed snippets.
//   - Socket path: /run/php/phpX.Y-fpm-POOL.sock
func buildPHPTemplate(req CreateRequest) (string, error) {
	if req.PHPVersion == "" {
		req.PHPVersion = "8.4"
	}
	pool := req.PHPPool
	if pool == "" {
		pool = strings.ReplaceAll(req.Domain, ".", "_")
	}
	socketPath := fmt.Sprintf("/run/php/php%s-fpm-%s.sock", req.PHPVersion, pool)

	docRoot := req.DocRoot
	if docRoot == "" {
		return "", fmt.Errorf("docRoot is required for php type")
	}

	prefix, listeners, ssl := buildSiteProtocol(req)

	config := fmt.Sprintf(`%s
server {
    %s
    server_name %s;

    root %s;
    index index.php index.html;

    include snippets/cloudflare-real-ip.conf;
    include snippets/security-headers.conf;
    include snippets/security-deny.conf;
    include snippets/gzip-brotli.conf;
    include snippets/static-cache.conf;

    %s

    location / {
        try_files $uri $uri/ /index.php?$query_string;
    }

    location ~ \.php$ {
        try_files $uri =404;
        fastcgi_split_path_info ^(.+\.php)(/.+)$;
        fastcgi_pass unix:%s;
        include snippets/php-fpm.conf;
    }

    location ~ /\.(?!well-known) {
        deny all;
    }

    access_log /var/log/nginx/%s-access.log combined;
    error_log  /var/log/nginx/%s-error.log warn;
}
`, prefix, listeners, req.Domain, docRoot, ssl, socketPath, req.Domain, req.Domain)

	return config, nil
}

// ---------- Reverse Proxy template ----------

// buildProxyTemplate generates a reverse proxy (Node.js / PM2) vhost config.
func buildProxyTemplate(req CreateRequest) (string, error) {
	if req.ProxyPass == "" {
		return "", fmt.Errorf("proxyPass is required for proxy type")
	}

	prefix, listeners, ssl := buildSiteProtocol(req)

	config := fmt.Sprintf(`%s
server {
    %s
    server_name %s;

    include snippets/cloudflare-real-ip.conf;
    include snippets/security-headers.conf;
    include snippets/gzip-brotli.conf;

    %s

    location / {
        proxy_pass %s;
        include snippets/proxy-params.conf;
    }

    location /api/health {
        proxy_pass %s;
        include snippets/proxy-params.conf;
        access_log off;
    }

    access_log /var/log/nginx/%s-access.log combined;
    error_log  /var/log/nginx/%s-error.log warn;
}
`, prefix, listeners, req.Domain, ssl, req.ProxyPass, req.ProxyPass, req.Domain, req.Domain)

	return config, nil
}

// ---------- Static Site template ----------

// buildStaticTemplate generates a config for serving plain static files.
func buildStaticTemplate(req CreateRequest) (string, error) {
	docRoot := req.DocRoot
	if docRoot == "" {
		return "", fmt.Errorf("docRoot is required for static type")
	}

	prefix, listeners, ssl := buildSiteProtocol(req)

	config := fmt.Sprintf(`%s
server {
    %s
    server_name %s;

    root %s;
    index index.html index.htm;

    include snippets/cloudflare-real-ip.conf;
    include snippets/security-headers.conf;
    include snippets/security-deny.conf;
    include snippets/gzip-brotli.conf;
    include snippets/static-cache.conf;

    %s

    location / {
        try_files $uri $uri/ =404;
    }

    location ~ /\.(?!well-known) {
        deny all;
    }

    access_log /var/log/nginx/%s-access.log combined;
    error_log  /var/log/nginx/%s-error.log warn;
}
`, prefix, listeners, req.Domain, docRoot, ssl, req.Domain, req.Domain)

	return config, nil
}

// ---------- Redirect template ----------

// buildRedirectTemplate generates a permanent redirect (301) config.
func buildRedirectTemplate(req CreateRequest) (string, error) {
	if req.RedirectTo == "" {
		return "", fmt.Errorf("redirectTo is required for redirect type")
	}

	// Ensure target has a scheme
	target := req.RedirectTo
	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		target = "https://" + target
	}

	http := fmt.Sprintf(`server {
    listen 80;
    listen [::]:80;
    server_name %s;

    include snippets/acme-challenge.conf;
    include snippets/cloudflare-real-ip.conf;

    location / {
        return 301 %s$request_uri;
    }
}
`, req.Domain, target)
	if !req.UseSSL {
		return http, nil
	}

	ssl := buildSSLBlock(req.Domain, req.CertPath, req.KeyPath, true)
	config := fmt.Sprintf(`%s

server {
    listen 443 ssl;
    listen [::]:443 ssl;
    http2 on;
    server_name %s;

    include snippets/cloudflare-real-ip.conf;
    include snippets/ssl-params.conf;

    %s

    location / {
        return 301 %s$request_uri;
    }

    access_log off;
    error_log /var/log/nginx/%s-error.log warn;
}
`, http, req.Domain, ssl, target, req.Domain)

	return config, nil
}

// ---------- shared helpers ----------

func buildSiteProtocol(req CreateRequest) (prefix, listeners, ssl string) {
	if !req.UseSSL {
		return "", "listen 80;\n    listen [::]:80;", ""
	}
	ssl = "include snippets/ssl-params.conf;\n    " + buildSSLBlock(req.Domain, req.CertPath, req.KeyPath, true)
	return buildHTTPRedirect(req.Domain), "listen 443 ssl;\n    listen [::]:443 ssl;\n    http2 on;", ssl
}

// buildHTTPRedirect returns an HTTP→HTTPS redirect server block.
// This is included at the top of PHP, proxy, and static templates.
func buildHTTPRedirect(domain string) string {
	return fmt.Sprintf(`server {
    listen 80;
    listen [::]:80;
    server_name %s;

    include snippets/acme-challenge.conf;
    include snippets/cloudflare-real-ip.conf;

    location / {
        return 301 https://$host$request_uri;
    }
}
`, domain)
}

// buildSSLBlock returns the ssl_certificate / ssl_certificate_key directives.
// Cert paths default to Let's Encrypt fullchain / privkey if not specified.
func buildSSLBlock(domain, certPath, keyPath string, useSSL bool) string {
	if !useSSL {
		return ""
	}
	if certPath == "" {
		certPath = fmt.Sprintf("/etc/letsencrypt/live/%s/fullchain.pem", domain)
	}
	if keyPath == "" {
		keyPath = fmt.Sprintf("/etc/letsencrypt/live/%s/privkey.pem", domain)
	}
	return fmt.Sprintf("ssl_certificate     %s;\n    ssl_certificate_key %s;", certPath, keyPath)
}
