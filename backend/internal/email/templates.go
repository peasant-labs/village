package email

import (
	"bytes"
	"fmt"
	"html/template"
	"strings"
	"time"
)

// The templates are plain text plus minimal HTML in the peasant labs voice:
// lowercase chrome, no images, no tracking pixels, no rewritten links. A
// collective's name and an inviter's handle are user content and are kept as
// they were written; the HTML part escapes them.

// InvitationNotice is what an invitation email says.
type InvitationNotice struct {
	// InviterHandle is the inviting owner's handle as the invitee may see it,
	// or empty when the owner is anonymous to them.
	InviterHandle  string
	CollectiveName string
	// InvitationURL is the deep link to the invitation. It carries no secret:
	// opening it requires signing in as the invitee.
	InvitationURL string
	// UnsubscribeURL is the signed-out page that turns invitation emails off.
	UnsubscribeURL string
	// OneClickUnsubscribeURL is the RFC 8058 POST target for mail clients.
	OneClickUnsubscribeURL string
}

var invitationHTML = template.Must(template.New("invitation").Parse(`<!doctype html>
<html><body style="font-family: sans-serif; font-size: 16px; line-height: 1.5;">
<p>{{.Lead}}</p>
<p>open it to accept or decline:<br><a href="{{.InvitationURL}}">{{.InvitationURL}}</a></p>
<p style="font-size: 14px;">you're getting this because you asked for invitation emails. <a href="{{.UnsubscribeURL}}">stop them</a>.</p>
<p style="font-size: 14px;">peasant labs</p>
</body></html>
`))

// Message renders the invitation email for one recipient.
func (n InvitationNotice) Message(to string) (Message, error) {
	name := oneLine(n.CollectiveName)
	lead := fmt.Sprintf("an owner of %s invited you to join it on village.", name)
	subject := fmt.Sprintf("you're invited to %s on village", name)
	if handle := oneLine(n.InviterHandle); handle != "" {
		lead = fmt.Sprintf("@%s invited you to %s on village.", handle, name)
		subject = fmt.Sprintf("@%s invited you to %s on village", handle, name)
	}
	text := lead + "\n\n" +
		"open it to accept or decline:\n" + n.InvitationURL + "\n\n" +
		"you're getting this because you asked for invitation emails. stop them:\n" + n.UnsubscribeURL + "\n\n" +
		"peasant labs\n"
	var html bytes.Buffer
	if err := invitationHTML.Execute(&html, struct {
		Lead           string
		InvitationURL  string
		UnsubscribeURL string
	}{lead, n.InvitationURL, n.UnsubscribeURL}); err != nil {
		return Message{}, fmt.Errorf("email.InvitationNotice.Message: render the HTML part: %w", err)
	}
	return Message{
		To: to, Subject: subject, Text: text, HTML: html.String(),
		Headers: map[string]string{
			"List-Unsubscribe":      "<" + n.OneClickUnsubscribeURL + ">",
			"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
		},
	}, nil
}

// Verification is what the address-verification email says.
type Verification struct {
	Token     string
	VerifyURL string
	Lifetime  time.Duration
}

var verificationHTML = template.Must(template.New("verification").Parse(`<!doctype html>
<html><body style="font-family: sans-serif; font-size: 16px; line-height: 1.5;">
<p>someone asked village to send collective invitations to this address. if it was you, confirm it:<br><a href="{{.VerifyURL}}">{{.VerifyURL}}</a></p>
<p>or paste this code in your village settings:<br><code>{{.Token}}</code></p>
<p>it expires in {{.Hours}} hours. if it wasn't you, ignore this email and nothing happens.</p>
<p style="font-size: 14px;">peasant labs</p>
</body></html>
`))

// Message renders the verification email.
func (v Verification) Message(to string) (Message, error) {
	hours := int(v.Lifetime / time.Hour)
	text := "someone asked village to send collective invitations to this address. if it was you, confirm it:\n" +
		v.VerifyURL + "\n\n" +
		"or paste this code in your village settings:\n" + v.Token + "\n\n" +
		fmt.Sprintf("it expires in %d hours. if it wasn't you, ignore this email and nothing happens.\n\n", hours) +
		"peasant labs\n"
	var html bytes.Buffer
	if err := verificationHTML.Execute(&html, struct {
		Token     string
		VerifyURL string
		Hours     int
	}{v.Token, v.VerifyURL, hours}); err != nil {
		return Message{}, fmt.Errorf("email.Verification.Message: render the HTML part: %w", err)
	}
	return Message{To: to, Subject: "confirm your email for village", Text: text, HTML: html.String()}, nil
}

// oneLine keeps user content from breaking a subject line.
func oneLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}
