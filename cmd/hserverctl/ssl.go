package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"
)

func runSSL(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl ssl status|list|get|action|issue")
	}
	switch args[0] {
	case "status":
		if len(args) != 1 {
			return errors.New("usage: hserverctl ssl status")
		}
		return printRequest(ctx, client, out, http.MethodGet, "/api/ssl/status", nil, true)
	case "list":
		node, err := parseOptionalNode("ssl list", args[1:])
		if err != nil {
			return err
		}
		endpoint := "/api/ssl/certificates"
		if node != "" {
			endpoint = "/api/nodes/" + url.PathEscape(node) + "/certificates"
		}
		return printRequest(ctx, client, out, http.MethodGet, endpoint, nil, true)
	case "get":
		if len(args) != 2 {
			return errors.New("usage: hserverctl ssl get DOMAIN")
		}
		domain, err := validateDomainName(args[1])
		if err != nil {
			return err
		}
		return printRequest(ctx, client, out, http.MethodGet, "/api/ssl/certificates/"+url.PathEscape(domain), nil, true)
	case "action":
		return runSSLAction(ctx, client, args[1:], out)
	case "issue":
		return runSSLIssue(ctx, client, args[1:], out)
	default:
		return fmt.Errorf("unknown ssl command %q", args[0])
	}
}

func runSSLAction(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("ssl action", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	node := flags.String("node", "", "managed node ID; omit for the local host")
	confirmed := flags.Bool("confirm", false, "confirm certificate renewal")
	wait := flags.Duration("wait", 20*time.Minute, "maximum certificate action wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 2 {
		return errors.New("usage: hserverctl ssl action [--confirm] [--node NODE] [--wait DURATION] NAME check|renew")
	}
	if *wait <= 0 {
		return errors.New("SSL action wait must be greater than zero")
	}
	name, err := validateDomainResourceIdentity(flags.Args()[0])
	if err != nil {
		return err
	}
	action := flags.Args()[1]
	if action != "check" && action != "renew" {
		return fmt.Errorf("unsupported SSL action %q", action)
	}
	remote := strings.TrimSpace(*node) != ""
	if !remote && action == "check" {
		return errors.New("SSL check is available for managed nodes; use ssl get for the local certificate")
	}
	if action == "renew" && !*confirmed {
		return errors.New("SSL renewal requires explicit --confirm")
	}
	endpoint := "/api/ssl/renew/" + url.PathEscape(name)
	if remote {
		endpoint = "/api/nodes/" + url.PathEscape(strings.TrimSpace(*node)) + "/certificates/" +
			url.PathEscape(name) + "/actions/" + action
	}
	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPost, endpoint, nil, true)
}

func runSSLIssue(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("ssl issue", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	domainValue := flags.String("domain", "", "certificate domain")
	emailValue := flags.String("email", "", "ACME account email")
	challenge := flags.String("challenge", "http-01", "ACME challenge: http-01 or dns-01")
	confirmed := flags.Bool("confirm", false, "confirm certificate issuance")
	wait := flags.Duration("wait", 20*time.Minute, "maximum issuance wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 || strings.TrimSpace(*domainValue) == "" || strings.TrimSpace(*emailValue) == "" {
		return errors.New("usage: hserverctl ssl issue --confirm --domain DOMAIN --email EMAIL [--challenge http-01|dns-01] [--wait DURATION]")
	}
	if !*confirmed {
		return errors.New("SSL issuance requires explicit --confirm")
	}
	if *wait <= 0 {
		return errors.New("SSL issuance wait must be greater than zero")
	}
	domain, err := validateDomainName(*domainValue)
	if err != nil {
		return err
	}
	email := strings.TrimSpace(*emailValue)
	parsedEmail, err := mail.ParseAddress(email)
	if err != nil || parsedEmail.Address != email {
		return errors.New("email must be a plain valid email address")
	}
	challengeType := strings.TrimSpace(*challenge)
	if challengeType != "http-01" && challengeType != "dns-01" {
		return errors.New("SSL challenge must be http-01 or dns-01")
	}
	payload := map[string]string{"domain": domain, "email": email, "challengeType": challengeType}
	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPost, "/api/ssl/issue", payload, true)
}
