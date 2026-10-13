package email

import "strings"

// provider is what arc5 knows of a mail service, found by the address's
// domain: its IMAP server and what its login needs.
type provider struct {
	name        string // "" for a domain arc5 does not know
	host        string
	port        int
	help        string // said on the password step
	refused     string // said when the server refuses the login
	oauthOnly   bool   // the service no longer accepts passwords over IMAP
	appPassword bool
}

const genericRefused = "The server refused the login. Check the user name and the password; many providers need an app password for mail programs rather than your usual password."

var gmail = provider{
	name: "Gmail", host: "imap.gmail.com", port: 993, appPassword: true,
	help: "Gmail needs an app password, not your Google password. Turn on 2-Step Verification for the account, " +
		"then create an app password at https://myaccount.google.com/apppasswords and paste its 16 letters here. " +
		"arc5 only reads mail with it. A Google Workspace account works only if its administrator allows app passwords and IMAP.",
	refused: "Gmail refused the login. Use an app password from https://myaccount.google.com/apppasswords, not your Google password; " +
		"creating one needs 2-Step Verification. A Workspace account also needs its administrator to allow app passwords and IMAP.",
}

func appPassword(name, host, page string) provider {
	return provider{
		name: name, host: host, port: 993, appPassword: true,
		help:    name + " needs an app password for mail programs, not your usual password. Create one at " + page + " and paste it here.",
		refused: name + " refused the login. Use an app password from " + page + ", not your usual password.",
	}
}

func plain(name, host string) provider {
	return provider{name: name, host: host, port: 993, help: "Your " + name + " password, or an app password if you made one for mail programs.", refused: genericRefused}
}

var (
	microsoft = provider{name: "Microsoft", oauthOnly: true}
	providers = map[string]provider{
		"gmail.com": gmail, "googlemail.com": gmail,
		"fastmail.com": appPassword("Fastmail", "imap.fastmail.com", "Settings, Privacy & Security, App passwords"),
		"fastmail.fm":  appPassword("Fastmail", "imap.fastmail.com", "Settings, Privacy & Security, App passwords"),
		"icloud.com":   appPassword("iCloud", "imap.mail.me.com", "https://account.apple.com (Sign-In and Security, App-Specific Passwords)"),
		"me.com":       appPassword("iCloud", "imap.mail.me.com", "https://account.apple.com (Sign-In and Security, App-Specific Passwords)"),
		"mac.com":      appPassword("iCloud", "imap.mail.me.com", "https://account.apple.com (Sign-In and Security, App-Specific Passwords)"),
		"yahoo.com":    appPassword("Yahoo", "imap.mail.yahoo.com", "Yahoo's Account Security page (Generate app password)"),
		"ymail.com":    appPassword("Yahoo", "imap.mail.yahoo.com", "Yahoo's Account Security page (Generate app password)"),
		"gmx.com":      plain("GMX", "imap.gmx.com"),
		"gmx.net":      plain("GMX", "imap.gmx.net"),
		"gmx.de":       plain("GMX", "imap.gmx.net"),
		"mailbox.org":  plain("mailbox.org", "imap.mailbox.org"),
		"yandex.ru":    appPassword("Yandex", "imap.yandex.com", "Yandex ID, Security, App passwords"),
		"yandex.com":   appPassword("Yandex", "imap.yandex.com", "Yandex ID, Security, App passwords"),
		"ya.ru":        appPassword("Yandex", "imap.yandex.com", "Yandex ID, Security, App passwords"),
		"zoho.com":     plain("Zoho", "imap.zoho.com"),
		"outlook.com":  microsoft, "hotmail.com": microsoft, "live.com": microsoft, "msn.com": microsoft,
	}
)

// providerFor is the provider of an address, or a guess at its server.
func providerFor(address string) provider {
	_, domain, _ := strings.Cut(strings.ToLower(strings.TrimSpace(address)), "@")
	if p, ok := providers[domain]; ok {
		return p
	}
	return provider{host: "imap." + domain, port: 993, help: "The password of this mailbox, or an app password if your provider asks for one.", refused: genericRefused}
}
