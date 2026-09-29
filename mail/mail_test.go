package mail

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCleanEveMailBody(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		expected string
	}{
		{
			name:     "basic formatting tags stripped and br to newline",
			raw:      `<font size="12" color="#bfffffff">Hello<br><br>This is a <b>test</b> message &amp; greetings.<br/></font>`,
			expected: "Hello\n\nThis is a test message & greetings.",
		},
		{
			name:     "preserve https link as markdown",
			raw:      `<font size="12">Join our Discord: <a href="https://discord.gg/abc">https://discord.gg/abc</a></font>`,
			expected: `Join our Discord: [https://discord.gg/abc](https://discord.gg/abc)`,
		},
		{
			name:     "preserve http link as markdown",
			raw:      `Visit <a href="http://eve-gate.net">our site</a> for details.`,
			expected: `Visit [our site](http://eve-gate.net) for details.`,
		},
		{
			name:     "strip non-http/https eve links like showinfo and fitting",
			raw:      `Meet at <a href="showinfo:1373//10000002">Jita IV - 4</a> in your <a href="fitting:1234:...">Rifter</a>.`,
			expected: `Meet at Jita IV - 4 in your Rifter.`,
		},
		{
			name:     "mixed http and non-http links",
			raw:      `Check <a href="showinfo:1373//10000002">Jita</a> and <a href="https://zkillboard.com">Zkill</a> and <a href="http://example.com">Example</a>.`,
			expected: `Check Jita and [Zkill](https://zkillboard.com) and [Example](http://example.com).`,
		},
		{
			name:     "nested formatting inside allowed link stripped",
			raw:      `<a href="https://example.com"><b>Bold Link</b></a>`,
			expected: `[Bold Link](https://example.com)`,
		},
		{
			name:     "case insensitivity of scheme and tags",
			raw:      `<a href="HTTPS://EXAMPLE.COM">Capital Scheme</a> and <A HREF="HTTP://EXAMPLE.COM">Capital Tag</A>`,
			expected: `[Capital Scheme](HTTPS://EXAMPLE.COM) and [Capital Tag](HTTP://EXAMPLE.COM)`,
		},
		{
			name:     "extra attributes in a tag handled",
			raw:      `<a target="_blank" href="https://example.com" class="external">Link</a>`,
			expected: `[Link](https://example.com)`,
		},
		{
			name:     "other schemes stripped",
			raw:      `Contact <a href="mailto:pilot@eve.com">pilot</a> or <a href="javascript:void(0)">click</a>.`,
			expected: `Contact pilot or click.`,
		},
		{
			name:     "anchor without href or empty href stripped",
			raw:      `Anchor <a name="top">Top</a> and <a href="">Empty</a>.`,
			expected: `Anchor Top and Empty.`,
		},
		{
			name: "eve mail with html anchors converted to markdown links",
			raw: `Hi Peeps<br><br>1. SEAT / Discord<br><br>SEAT is linked to Discord, without logging into SEAT you CANNOT use voice comms. It is that simple, so when you join, log into this link > <a href="http://esi.cultofmagik.org/">Click here</a> < and sign in, that's all you have to do, when you login to Discord, change your name to the char you have in corporation or the bot will not assign roles to you. So get it done. <br><br>2. Wanderer<br><br>We are not longer posting the system in MagikMain MOTD for the C2 entrance, if you are in the Wormhole you should be logged into Wanderer, you can find the link here > <a href="http://wh.cultofmagik.org/home">Click Here</a> < In Wanderer you will be able to see the current C2 entrance and connecting holes. <br><br>3. Allies Discord <br><br>We will be working closely with  Prometheus Rising. Moving forwards, it is run by a close friend of mine, so to this end, we will be joining their Discord for join ops, please join their Discord ASAP, you can find the link here > <a href="http://discord.gg/F5dYbNuue">Discord Link</a> < - Please join <br><br>4. Wormhole Ships<br><br>If you are looking to live in the wormhole, you need have a Kikimora or armor destroyer, a Drake and or DNI/Nighthawk or a shield fitted battlecruiser for system defence, you can find these fits in the alliance bulletins<br><br>That's all for now, thanks for being awesome and keeping this corp respectful and casual<br><br>Regards<br><br>Demon`,
			expected: "Hi Peeps\n\n1. SEAT / Discord\n\nSEAT is linked to Discord, without logging into SEAT you CANNOT use voice comms. It is that simple, so when you join, log into this link > [Click here](http://esi.cultofmagik.org/) < and sign in, that's all you have to do, when you login to Discord, change your name to the char you have in corporation or the bot will not assign roles to you. So get it done. \n\n2. Wanderer\n\nWe are not longer posting the system in MagikMain MOTD for the C2 entrance, if you are in the Wormhole you should be logged into Wanderer, you can find the link here > [Click Here](http://wh.cultofmagik.org/home) < In Wanderer you will be able to see the current C2 entrance and connecting holes. \n\n3. Allies Discord \n\nWe will be working closely with  Prometheus Rising. Moving forwards, it is run by a close friend of mine, so to this end, we will be joining their Discord for join ops, please join their Discord ASAP, you can find the link here > [Discord Link](http://discord.gg/F5dYbNuue) < - Please join \n\n4. Wormhole Ships\n\nIf you are looking to live in the wormhole, you need have a Kikimora or armor destroyer, a Drake and or DNI/Nighthawk or a shield fitted battlecruiser for system defence, you can find these fits in the alliance bulletins\n\nThat's all for now, thanks for being awesome and keeping this corp respectful and casual\n\nRegards\n\nDemon",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleaned := CleanEveMailBody(tt.raw)
			if cleaned != tt.expected {
				t.Fatalf("expected:\n%q\ngot:\n%q", tt.expected, cleaned)
			}
		})
	}
}

func TestSendEveMail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/characters/12345/mail") {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req SendMailRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if req.Subject != "Test Subject" || req.Recipients[0].RecipientID != 999 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	client := server.Client()
	// Test sending with character ID 12345
	// In the real code, it points to https://esi.evetech.net, but we can verify param validation
	err := SendEveMail(nil, 12345, 999, "Test Subject", "Test Body")
	if err == nil {
		t.Fatal("expected error with nil client")
	}

	err = SendEveMail(client, 0, 999, "Test Subject", "Test Body")
	if err == nil {
		t.Fatal("expected error with 0 sender character ID and empty config")
	}
}
