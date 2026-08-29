package mail

// autoconfig.go — Generate email client auto-discovery XML documents.
//
// Two standards are supported:
//   - Mozilla Autoconfig (Thunderbird, Betterbird, …)
//       GET  /mail/autoconfig/mail/config-v1.1.xml?emailaddress=user@domain
//       GET  /.well-known/autoconfig/mail/config-v1.1.xml
//   - Microsoft Autodiscover (Outlook, …)
//       POST /autodiscover/autodiscover.xml
//
// Both endpoints are intentionally served WITHOUT authentication so that mail
// clients can call them before the user has logged in.

import (
	"encoding/xml"
	"strings"
)

// ── Mozilla Autoconfig ────────────────────────────────────────────────────────

// AutoconfigXML is the root element of Mozilla's ISPDB autoconfig format v1.1.
type AutoconfigXML struct {
	XMLName     xml.Name          `xml:"clientConfig"`
	Version     string            `xml:"version,attr"`
	EmailProvider AutoconfigProvider `xml:"emailProvider"`
}

// AutoconfigProvider describes a single mail provider.
type AutoconfigProvider struct {
	ID             string             `xml:"id,attr"`
	Domain         string             `xml:"domain"`
	DisplayName    string             `xml:"displayName"`
	DisplayShortName string           `xml:"displayShortName"`
	IncomingServer []AutoconfigServer `xml:"incomingServer"`
	OutgoingServer []AutoconfigServer `xml:"outgoingServer"`
}

// AutoconfigServer describes a single IMAP/POP3/SMTP server entry.
type AutoconfigServer struct {
	Type           string `xml:"type,attr"`
	Hostname       string `xml:"hostname"`
	Port           int    `xml:"port"`
	SocketType     string `xml:"socketType"`     // SSL, STARTTLS, plain
	Authentication string `xml:"authentication"` // password-cleartext, OAuth2, …
	Username       string `xml:"username"`       // %EMAILADDRESS% or %EMAILLOCALPART%
}

// GenerateAutoconfig builds a Mozilla Autoconfig XML document for the given
// domain.  It assumes standard Stalwart port/TLS layout:
//   - IMAP:   993 (SSL/TLS)
//   - POP3:   995 (SSL/TLS)   — included for broad client support
//   - SMTP:   465 (SSL/TLS) submission
//   - SMTP:   587 (STARTTLS) submission
func (s *Service) GenerateAutoconfig(domain string) ([]byte, error) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	mailHost := "mail." + domain

	cfg := AutoconfigXML{
		Version: "1.1",
		EmailProvider: AutoconfigProvider{
			ID:               domain,
			Domain:           domain,
			DisplayName:      domain,
			DisplayShortName: domain,
			IncomingServer: []AutoconfigServer{
				{
					Type:           "imap",
					Hostname:       mailHost,
					Port:           993,
					SocketType:     "SSL",
					Authentication: "password-cleartext",
					Username:       "%EMAILADDRESS%",
				},
				{
					Type:           "pop3",
					Hostname:       mailHost,
					Port:           995,
					SocketType:     "SSL",
					Authentication: "password-cleartext",
					Username:       "%EMAILADDRESS%",
				},
			},
			OutgoingServer: []AutoconfigServer{
				{
					Type:           "smtp",
					Hostname:       mailHost,
					Port:           465,
					SocketType:     "SSL",
					Authentication: "password-cleartext",
					Username:       "%EMAILADDRESS%",
				},
				{
					Type:           "smtp",
					Hostname:       mailHost,
					Port:           587,
					SocketType:     "STARTTLS",
					Authentication: "password-cleartext",
					Username:       "%EMAILADDRESS%",
				},
			},
		},
	}

	data, err := xml.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), data...), nil
}

// ── Microsoft Autodiscover ────────────────────────────────────────────────────

// AutodiscoverRequest is the minimal XML Outlook sends to the endpoint.
type AutodiscoverRequest struct {
	XMLName       xml.Name `xml:"Autodiscover"`
	Request       struct {
		EMailAddress string `xml:"EMailAddress"`
	} `xml:"Request"`
}

// AutodiscoverResponse is the XML Outlook expects in return.
type AutodiscoverResponse struct {
	XMLName  xml.Name             `xml:"Autodiscover"`
	XMLNS    string               `xml:"xmlns,attr"`
	Response AutodiscoverAccount  `xml:"Response"`
}

// AutodiscoverAccount contains the IMAP/SMTP account settings.
type AutodiscoverAccount struct {
	XMLNS      string                   `xml:"xmlns,attr"`
	Account    AutodiscoverAccountBlock `xml:"Account"`
}

// AutodiscoverAccountBlock holds all protocol settings.
type AutodiscoverAccountBlock struct {
	AccountType string                     `xml:"AccountType"`
	Action      string                     `xml:"Action"`
	Protocol    []AutodiscoverProtocol     `xml:"Protocol"`
}

// AutodiscoverProtocol is one IMAP/SMTP block.
type AutodiscoverProtocol struct {
	Type                string `xml:"Type"`
	Server              string `xml:"Server"`
	Port                int    `xml:"Port"`
	LoginName           string `xml:"LoginName"`
	DomainRequired      string `xml:"DomainRequired"`
	SPA                 string `xml:"SPA"`           // Secure Password Authentication: off
	SSL                 string `xml:"SSL"`           // on / off
	AuthRequired        string `xml:"AuthRequired"`  // on
}

// GenerateAutodiscover builds a Microsoft Autodiscover XML response for the
// given domain and email address.
//
// loginName may be the full email address (Stalwart default) or just the local
// part; we use the full address to match Stalwart's principal naming.
func (s *Service) GenerateAutodiscover(domain, emailAddress string) ([]byte, error) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	mailHost := "mail." + domain

	if emailAddress == "" {
		emailAddress = "user@" + domain
	}

	resp := AutodiscoverResponse{
		XMLNS: "http://schemas.microsoft.com/exchange/autodiscover/responseschema/2006",
		Response: AutodiscoverAccount{
			XMLNS: "http://schemas.microsoft.com/exchange/autodiscover/outlook/responseschema/2006a",
			Account: AutodiscoverAccountBlock{
				AccountType: "email",
				Action:      "settings",
				Protocol: []AutodiscoverProtocol{
					{
						Type:           "IMAP",
						Server:         mailHost,
						Port:           993,
						LoginName:      emailAddress,
						DomainRequired: "off",
						SPA:            "off",
						SSL:            "on",
						AuthRequired:   "on",
					},
					{
						Type:           "SMTP",
						Server:         mailHost,
						Port:           465,
						LoginName:      emailAddress,
						DomainRequired: "off",
						SPA:            "off",
						SSL:            "on",
						AuthRequired:   "on",
					},
				},
			},
		},
	}

	data, err := xml.MarshalIndent(resp, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), data...), nil
}
