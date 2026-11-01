package mail

import (
	"bytes"
	"fmt"
	"html/template"
	"strconv"
)

type Message struct {
	Subject, HTML, Text string
}

var orgInviteTmpl = template.Must(template.New("").Parse(`
    <div style="font-family:ui-sans-serif,system-ui,sans-serif;max-width:480px;margin:0 auto;padding:24px;color:#171717">
      <h1 style="font-size:20px;font-weight:600;margin:0 0 8px">Join {{.OrgName}}</h1>
      <p style="font-size:14px;color:#525252;margin:0 0 24px">
        <strong>{{.InviterName}}</strong> invited you to join <strong>{{.OrgName}}</strong> as <strong>{{.RoleLabel}}</strong> on Hectix.
      </p>
      <a href="{{.AcceptURL}}"
         style="display:inline-block;background:#171717;color:#fff;text-decoration:none;
                padding:10px 20px;border-radius:8px;font-size:14px;font-weight:500">
        Accept invitation
      </a>
      <p style="font-size:12px;color:#a3a3a3;margin:24px 0 0">
        Sign in with the Google account for <strong>{{.To}}</strong>. This link expires in 7 days.
      </p>
    </div>`))

var payoutProcessedTmpl = template.Must(template.New("").Parse(`
    <div style="font-family:ui-sans-serif,system-ui,sans-serif;max-width:480px;margin:0 auto;padding:24px;color:#171717">
      <h1 style="font-size:20px;font-weight:600;margin:0 0 8px">Payout sent</h1>
      <p style="font-size:14px;color:#525252;margin:0 0 16px">
        We've sent your payout of <strong>{{.AmountLabel}}</strong> for <strong>{{.OrgName}}</strong>.
        It may take a short while to appear in your bank account.
      </p>
      {{if .ReceiptURL}}<a href="{{.ReceiptURL}}" style="display:inline-block;background:#171717;color:#fff;text-decoration:none;padding:10px 20px;border-radius:8px;font-size:14px;font-weight:500">View receipt</a>{{end}}
    </div>`))

var payoutRejectedTmpl = template.Must(template.New("").Parse(`
    <div style="font-family:ui-sans-serif,system-ui,sans-serif;max-width:480px;margin:0 auto;padding:24px;color:#171717">
      <h1 style="font-size:20px;font-weight:600;margin:0 0 8px">Payout not processed</h1>
      <p style="font-size:14px;color:#525252;margin:0 0 12px">
        We couldn't process your payout of <strong>{{.AmountLabel}}</strong> for <strong>{{.OrgName}}</strong>.
      </p>
      <p style="font-size:14px;color:#525252;margin:0 0 16px;padding:12px;background:#fef2f2;border-radius:8px">
        <strong>Reason:</strong> {{.Reason}}
      </p>
      <p style="font-size:13px;color:#a3a3a3;margin:0">
        Please review and submit a new payout request from your Finance page.
      </p>
    </div>`))

var adminInviteTmpl = template.Must(template.New("").Parse(`
    <div style="font-family:ui-sans-serif,system-ui,sans-serif;max-width:480px;margin:0 auto;padding:24px;color:#171717">
      <h1 style="font-size:20px;font-weight:600;margin:0 0 8px">Hectix Admin</h1>
      <p style="font-size:14px;color:#525252;margin:0 0 24px">
        You've been invited to join the team as <strong>{{.RoleLabel}}</strong>.
      </p>
      <a href="{{.AcceptURL}}"
         style="display:inline-block;background:#171717;color:#fff;text-decoration:none;
                padding:10px 20px;border-radius:8px;font-size:14px;font-weight:500">
        Accept invitation
      </a>
      <p style="font-size:12px;color:#a3a3a3;margin:24px 0 0">
        Sign in with the Google account for <strong>{{.To}}</strong>. This link expires in 7 days.
      </p>
    </div>`))

var eticketTmpl = template.Must(template.New("").Parse(`
    <div style="font-family:ui-sans-serif,system-ui,sans-serif;max-width:480px;margin:0 auto;padding:24px;color:#171717">
      <h1 style="font-size:20px;font-weight:600;margin:0 0 8px">You're going to {{.EventName}}! 🎟️</h1>
      <p style="font-size:14px;color:#525252;margin:0 0 16px">
        Your payment is confirmed. Your e-ticket is attached to this email as a PDF —
        show the QR code at the entrance to scan in.
      </p>
      <table style="font-size:14px;color:#171717;border-collapse:collapse;margin:0 0 16px">
        <tr><td style="padding:2px 16px 2px 0;color:#737373">Order</td><td style="font-weight:600">{{.ReferenceNumber}}</td></tr>
        <tr><td style="padding:2px 16px 2px 0;color:#737373">Tickets</td><td style="font-weight:600">{{.Quantity}}</td></tr>
        <tr><td style="padding:2px 16px 2px 0;color:#737373">Total</td><td style="font-weight:600">Rp{{.Total}}</td></tr>
      </table>
      <p style="font-size:12px;color:#a3a3a3;margin:24px 0 0">Hectix — Find Your Crowd</p>
    </div>`))

func render(t *template.Template, data any) string {
	var b bytes.Buffer
	if err := t.Execute(&b, data); err != nil {
		panic(err)
	}
	return b.String()
}

func OrgInvite(to, inviterName, orgName, roleLabel, acceptURL string) Message {
	return Message{
		Subject: fmt.Sprintf("%s invited you to join %s on Hectix", inviterName, orgName),
		Text:    fmt.Sprintf("%s invited you to join %s on Hectix as %s.\n\nAccept: %s\n\nThis link expires in 7 days.", inviterName, orgName, roleLabel, acceptURL),
		HTML: render(orgInviteTmpl, map[string]string{
			"To": to, "InviterName": inviterName, "OrgName": orgName, "RoleLabel": roleLabel, "AcceptURL": acceptURL,
		}),
	}
}

func PayoutProcessed(orgName, amountLabel, receiptURL string) Message {
	text := fmt.Sprintf("Good news — we've sent your payout of %s for %s.\n\n", amountLabel, orgName)
	if receiptURL != "" {
		text += fmt.Sprintf("View the transfer receipt: %s\n\n", receiptURL)
	}
	text += "It may take a short while to appear in your bank account."
	return Message{
		Subject: fmt.Sprintf("Your payout of %s has been sent", amountLabel),
		Text:    text,
		HTML: render(payoutProcessedTmpl, map[string]string{
			"OrgName": orgName, "AmountLabel": amountLabel, "ReceiptURL": receiptURL,
		}),
	}
}

func PayoutRejected(orgName, amountLabel, reason string) Message {
	return Message{
		Subject: "We couldn't process your payout",
		Text: fmt.Sprintf("We couldn't process your payout of %s for %s.\n\n", amountLabel, orgName) +
			fmt.Sprintf("Reason: %s\n\n", reason) +
			"Please review and submit a new payout request.",
		HTML: render(payoutRejectedTmpl, map[string]string{
			"OrgName": orgName, "AmountLabel": amountLabel, "Reason": reason,
		}),
	}
}

func AdminInvite(to, roleLabel, acceptURL string) Message {
	return Message{
		Subject: "You've been invited to Hectix Admin",
		Text:    fmt.Sprintf("You've been invited to join Hectix Admin as %s.\n\nAccept: %s\n\nThis link expires in 7 days.", roleLabel, acceptURL),
		HTML:    render(adminInviteTmpl, map[string]string{"To": to, "RoleLabel": roleLabel, "AcceptURL": acceptURL}),
	}
}

type EticketData struct {
	EventName       string
	ReferenceNumber string
	Quantity        int
	Total           int64
}

func Eticket(t EticketData) Message {
	return Message{
		Subject: fmt.Sprintf("Your tickets for %s 🎟️", t.EventName),
		Text: fmt.Sprintf("You're going to %s!\n\n", t.EventName) +
			fmt.Sprintf("Order: %s\n", t.ReferenceNumber) +
			fmt.Sprintf("Tickets: %d\n\n", t.Quantity) +
			"Your e-ticket is attached as a PDF. Show the QR code at the entrance to scan in.",
		HTML: render(eticketTmpl, map[string]string{
			"EventName": t.EventName, "ReferenceNumber": t.ReferenceNumber,
			"Quantity": strconv.Itoa(t.Quantity), "Total": formatThousands(t.Total),
		}),
	}
}

func EticketFilename(referenceNumber string) string {
	return "hectix-ticket-" + referenceNumber + ".pdf"
}

func formatThousands(n int64) string {
	s := strconv.FormatInt(n, 10)
	sign := ""
	if s[0] == '-' {
		sign, s = "-", s[1:]
	}
	var b []byte
	for i := range len(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			b = append(b, '.')
		}
		b = append(b, s[i])
	}
	return sign + string(b)
}
