package notify

import (
	"fmt"
	"mmt-delivery/pkg/env"
	"net/smtp"
	"strings"
)

// EmailClient represents an email client
type EmailClient struct {
	smtpHost     string
	smtpPort     string
	username     string
	password     string
	fromAddress  string
}

// EmailMessage represents an email message
type EmailMessage struct {
	To      []string
	Cc      []string
	Subject string
	Body    string
	IsHTML  bool
}

// NewEmailClient creates a new email client using the mail service destination
func NewEmailClient() (*EmailClient, error) {
	dest, err := env.GetDestination("Mail_Server")
	if err != nil {
		return nil, fmt.Errorf("failed to get mail service destination: %s", err)
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
func SendEmail(to []string, subject string, body string, isHTML bool) error {
	client, err := NewEmailClient()
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

	// Construct email headers
	var headers []string
	headers = append(headers, fmt.Sprintf("From: %s", c.fromAddress))
	headers = append(headers, fmt.Sprintf("To: %s", strings.Join(msg.To, ";")))
	if len(msg.Cc) > 0 {
		headers = append(headers, fmt.Sprintf("Cc: %s", strings.Join(msg.Cc, ";")))
	}
	headers = append(headers, fmt.Sprintf("Subject: %s", msg.Subject))

	// Set content type
	if msg.IsHTML {
		headers = append(headers, "MIME-version: 1.0")
		headers = append(headers, "Content-Type: text/html; charset=\"UTF-8\"")
	} else {
		headers = append(headers, "MIME-version: 1.0")
		headers = append(headers, "Content-Type: text/plain; charset=\"UTF-8\"")
	}

	// Combine headers and body
	message := strings.Join(headers, "\r\n") + "\r\n\r\n" + msg.Body

	// SMTP authentication
	auth := smtp.PlainAuth("", c.username, c.password, c.smtpHost)

	// Send email
	smtpAddr := fmt.Sprintf("%s:%s", c.smtpHost, c.smtpPort)
	err := smtp.SendMail(smtpAddr, auth, c.fromAddress, msg.To, []byte(message))
	if err != nil {
		env.Logger().Error("Failed to send email: %s", err)
		return fmt.Errorf("failed to send email: %s", err)
	}

	env.Logger().Info("Email sent successfully to %v", msg.To)
	return nil
}

// SendDeliveryNotification sends a delivery notification email
func SendDeliveryNotification(to []string, deliveryRequestID uint, status string, message string) error {
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

	return SendEmail(to, subject, body, true)
}

// SendApprovalRequest sends an approval request email
func SendApprovalRequest(to []string, deliveryRequestID uint, requestor string, description string) error {
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

	return SendEmail(to, subject, body, true)
}

// SendDeploymentNotification sends a deployment notification email
func SendDeploymentNotification(to []string, tenantName string, artifacts []string, status string) error {
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

	return SendEmail(to, subject, body, true)
}
