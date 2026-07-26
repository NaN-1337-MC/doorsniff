package login

import "testing"

func TestDetectMode(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want LoginMode
	}{
		{
			name: "whitelist simple text",
			in:   `You are not whitelisted on this server`,
			want: LoginModeWhitelisted,
		},
		{
			name: "whitelist JSON text component",
			in:   `{"text":"You are not whitelisted on this server"}`,
			want: LoginModeWhitelisted,
		},
		{
			name: "whitelist enabled JSON",
			in:   `{"text":"Whitelist is enabled"}`,
			want: LoginModeWhitelisted,
		},
		{
			name: "whitelist extra array",
			in:   `{"text":"","extra":[{"text":"Not on the whitelist"}]}`,
			want: LoginModeWhitelisted,
		},
		{
			name: "whitelist case insensitive",
			in:   `WHITELIST`,
			want: LoginModeWhitelisted,
		},
		{
			name: "ban simple text",
			in:   `You are banned from this server`,
			want: LoginModeBanned,
		},
		{
			name: "ban JSON",
			in:   `{"text":"You have been banned. Reason: griefing"}`,
			want: LoginModeBanned,
		},
		{
			name: "server full",
			in:   `Server is full`,
			want: LoginModeFull,
		},
		{
			name: "server full JSON",
			in:   `{"text":"The server is currently full"}`,
			want: LoginModeFull,
		},
		{
			name: "kicked JSON",
			in:   `{"text":"Kicked by an operator"}`,
			want: LoginModeKicked,
		},
		{
			name: "you have been kicked",
			in:   `You have been kicked from the server`,
			want: LoginModeKicked,
		},
		{
			name: "unknown message falls back to disconnect",
			in:   `Some random disconnect reason`,
			want: LoginModeDisconnect,
		},
		{
			name: "empty string returns unknown",
			in:   ``,
			want: LoginModeUnknown,
		},
		{
			name: "plain string reason",
			in:   `not whitelisted`,
			want: LoginModeWhitelisted,
		},
		{
			name: "translate component outdated_client with version",
			in:   `{"translate":"multiplayer.disconnect.outdated_client","with":["1.21.11"]}`,
			want: LoginModeDisconnect,
		},
		{
			name: "translate component outdated_server with version",
			in:   `{"translate":"multiplayer.disconnect.outdated_server","with":["1.21.11"]}`,
			want: LoginModeDisconnect,
		},
		{
			name: "Forge required text",
			in:   `This server has mods that require Forge to be installed on the client.`,
			want: LoginModeDisconnect,
		},
		{
			name: "FML required text",
			in:   `This server has mods that require FML/Forge to be installed on the client.`,
			want: LoginModeDisconnect,
		},
		{
			name: "Spanish outdated client",
			in:   `¡Cliente desactualizado! Por favor usa la versión 1.21.11`,
			want: LoginModeDisconnect,
		},
		{
			name: "Forge server crash IndexOutOfBoundsException",
			in:   `disconnect.genericReason Internal Exception: io.netty.handler.codec.DecoderException: java.lang.IndexOutOfBoundsException: readerIndex(20) + length(8) exceeds writerIndex(27)`,
			want: LoginModeDisconnect,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DetectMode(tc.in)
			if got != tc.want {
				t.Errorf("DetectMode(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}