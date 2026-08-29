package php

import "fmt"

// BuildPoolConfig generates a PHP-FPM pool configuration file content
// based on the provided CreatePoolRequest.
//
// Socket path pattern: /run/php/phpX.Y-fpm-POOLNAME.sock
// Security defaults: listen.owner/group = www-data, open_basedir locked to domain root.
func BuildPoolConfig(req CreatePoolRequest) string {
	socketPath := fmt.Sprintf("/run/php/php%s-fpm-%s.sock", req.Version, req.Name)

	group := req.Group
	if group == "" {
		group = "www-data"
	}

	pm := req.PM
	if pm == "" {
		pm = "dynamic"
	}

	openBasedir := ""
	if req.DomainRoot != "" {
		openBasedir = fmt.Sprintf("%s:/tmp/:/usr/share/php/", req.DomainRoot)
	}

	config := fmt.Sprintf(`[%s]
user = %s
group = %s

listen = %s
listen.owner = www-data
listen.group = www-data
listen.mode = 0660

pm = %s
pm.max_children = %d
`,
		req.Name,
		req.User,
		group,
		socketPath,
		pm,
		req.PMSettings.MaxChildren,
	)

	// pm.start_servers and spare servers are only relevant for dynamic/ondemand
	switch pm {
	case "dynamic":
		config += fmt.Sprintf("pm.start_servers = %d\n", req.PMSettings.StartServers)
		config += fmt.Sprintf("pm.min_spare_servers = %d\n", req.PMSettings.MinSpareServers)
		config += fmt.Sprintf("pm.max_spare_servers = %d\n", req.PMSettings.MaxSpareServers)
	case "ondemand":
		idleTimeout := req.PMSettings.ProcessIdleTimeout
		if idleTimeout == "" {
			idleTimeout = "10s"
		}
		config += fmt.Sprintf("pm.process_idle_timeout = %s\n", idleTimeout)
	}

	maxReq := req.PMSettings.MaxRequests
	if maxReq == 0 {
		maxReq = 500
	}
	config += fmt.Sprintf("pm.max_requests = %d\n", maxReq)

	config += `
chdir = /

catch_workers_output = yes
decorate_workers_output = no
`

	if openBasedir != "" {
		config += fmt.Sprintf(`
; Security
php_admin_value[open_basedir] = %s
php_admin_value[disable_functions] = opcache_get_status
`, openBasedir)
	}

	config += `
; Performance
php_value[opcache.memory_consumption] = 128
php_value[opcache.interned_strings_buffer] = 64
php_value[opcache.max_accelerated_files] = 10000
php_value[opcache.revalidate_freq] = 2
php_value[opcache.max_wasted_percentage] = 15
`

	return config
}
