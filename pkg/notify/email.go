package notify

import (
	"context"
	"crypto/tls"
	"fmt"
	"mmt-delivery/pkg/cf"
	"mmt-delivery/pkg/env"
	"strings"

	"gopkg.in/gomail.v2"
)

// EmailClient represents an email client
type EmailClient struct {
	smtpHost    string
	smtpPort    string
	username    string
	password    string
	fromAddress string
}

// EmailMessage represents an email message
type EmailMessage struct {
	To      []string
	Cc      []string
	Subject string
	Body    string
	IsHTML  bool
}

// NewEmailClient creates a new email client by resolving the given destination name.
func NewEmailClient(resolver *cf.DestinationServiceClient, destName string) (*EmailClient, error) {
	if destName == "" {
		return nil, fmt.Errorf("mail service destination name is empty")
	}
	dest, err := resolver.GetDestination(context.Background(), destName)
	if err != nil {
		return nil, fmt.Errorf("failed to get mail service destination '%s': %s", destName, err)
	}
	if dest == nil {
		return nil, fmt.Errorf("mail service destination '%s' not found", destName)
	}

	if dest.User == "" || dest.Password == "" {
		return nil, fmt.Errorf("mail service destination missing credentials")
	}

	// Clean URL - remove https:// prefix if present
	smtpHost := dest.URL
	smtpHost = strings.TrimPrefix(smtpHost, "https://")
	smtpHost = strings.TrimPrefix(smtpHost, "http://")

	// Use Port from destination if available, otherwise use default
	smtpPort := "587" // default submission port
	if dest.Port != "" {
		smtpPort = dest.Port
	}

	return &EmailClient{
		smtpHost:    smtpHost,
		smtpPort:    smtpPort,
		username:    dest.User,
		password:    dest.Password,
		fromAddress: dest.User, // Username is already the email address
	}, nil
}

// SendEmail sends an email to the specified users
func SendEmail(resolver *cf.DestinationServiceClient, destName string, to []string, subject string, body string, isHTML bool) error {
	client, err := NewEmailClient(resolver, destName)
	if err != nil {
		env.Logger().Error("Failed to create email client: %s", err)
		return err
	}

	message := &EmailMessage{
		To:      to,
		Subject: subject,
		Body:    body,
		IsHTML:  isHTML,
	}

	return client.Send(message)
}

// Send sends an email message
func (c *EmailClient) Send(msg *EmailMessage) error {
	if len(msg.To) == 0 {
		return fmt.Errorf("no recipients specified")
	}

	// Log connection details for debugging
	smtpAddr := fmt.Sprintf("%s:%s", c.smtpHost, c.smtpPort)
	env.Logger().Info("Attempting to send email via SMTP: %s", smtpAddr)
	env.Logger().Info("SMTP auth user: %s (password length: %d)", c.username, len(c.password))

	// Create a new message
	m := gomail.NewMessage()
	m.SetHeader("From", c.fromAddress)
	m.SetHeader("To", msg.To...)
	if len(msg.Cc) > 0 {
		m.SetHeader("Cc", msg.Cc...)
	}
	m.SetHeader("Subject", msg.Subject)

	// Set body
	if msg.IsHTML {
		m.SetBody("text/html", msg.Body)
	} else {
		m.SetBody("text/plain", msg.Body)
	}

	// Create SMTP dialer with TLS support
	port := 587
	if _, err := fmt.Sscanf(c.smtpPort, "%d", &port); err != nil {
		env.Logger().Warn("Failed to parse port %s, using default 587", c.smtpPort)
	}

	dialer := gomail.NewDialer(c.smtpHost, port, c.username, c.password)
	dialer.TLSConfig = &tls.Config{
		InsecureSkipVerify: false,
		ServerName:         c.smtpHost,
	}

	// Send email with timeout
	if err := dialer.DialAndSend(m); err != nil {
		env.Logger().Error("Failed to send email via %s: %s", smtpAddr, err)
		return fmt.Errorf("failed to send email: %s", err)
	}

	env.Logger().Info("Email sent successfully to %v", msg.To)
	return nil
}

// SendDeliveryNotification sends a delivery notification email
func SendDeliveryNotification(resolver *cf.DestinationServiceClient, destName string, to []string, deliveryRequestID uint, status string, message string) error {
	subject := fmt.Sprintf("Delivery Request #%d - %s", deliveryRequestID, status)

	body := fmt.Sprintf(`
<html>
<body>
	<h2>Delivery Request #%d</h2>
	<p><strong>Status:</strong> %s</p>
	<p><strong>Message:</strong></p>
	<pre>%s</pre>
	<hr>
	<p><small>This is an automated notification from the MMT Delivery System.</small></p>
</body>
</html>
`, deliveryRequestID, status, message)

	return SendEmail(resolver, destName, to, subject, body, true)
}

// SendApprovalRequest sends an approval request email
func SendApprovalRequest(resolver *cf.DestinationServiceClient, destName string, to []string, deliveryRequestID uint, requestor string, description string) error {
	subject := fmt.Sprintf("Approval Required: Delivery Request #%d", deliveryRequestID)

	body := fmt.Sprintf(`
<html>
<body>
	<h2>Approval Requested</h2>
	<p>A delivery request requires your approval:</p>
	<ul>
		<li><strong>Request ID:</strong> #%d</li>
		<li><strong>Requestor:</strong> %s</li>
		<li><strong>Description:</strong> %s</li>
	</ul>
	<p>Please review and approve the request in the MMT Delivery System.</p>
	<hr>
	<p><small>This is an automated notification from the MMT Delivery System.</small></p>
</body>
</html>
`, deliveryRequestID, requestor, description)

	return SendEmail(resolver, destName, to, subject, body, true)
}

// SendDeploymentNotification sends a deployment notification email
func SendDeploymentNotification(resolver *cf.DestinationServiceClient, destName string, to []string, tenantName string, artifacts []string, status string) error {
	subject := fmt.Sprintf("Deployment %s - %s", status, tenantName)

	artifactList := ""
	for _, artifact := range artifacts {
		artifactList += fmt.Sprintf("<li>%s</li>", artifact)
	}

	body := fmt.Sprintf(`
<html>
<body>
	<h2>Deployment %s</h2>
	<p><strong>Tenant:</strong> %s</p>
	<p><strong>Status:</strong> %s</p>
	<p><strong>Artifacts:</strong></p>
	<ul>
		%s
	</ul>
	<hr>
	<p><small>This is an automated notification from the MMT Delivery System.</small></p>
</body>
</html>
`, status, tenantName, status, artifactList)

	return SendEmail(resolver, destName, to, subject, body, true)
}
