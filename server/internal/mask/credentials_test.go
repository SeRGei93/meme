package mask_test

import (
	"strings"
	"testing"

	"github.com/SeRGei93/meme/server/internal/mask"
)

func TestTextMasksCredentialsAndPrivateKeys(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "bearer header",
			input: "Authorization: Bearer abcdef0123456789ABCDEF",
			want:  "Authorization: Bearer [REDACTED]",
		},
		{
			name:  "basic header",
			input: "Authorization: Basic dXNlcjpwYXNzd29yZA==",
			want:  "Authorization: Basic [REDACTED]",
		},
		{
			name:  "short basic header",
			input: "Authorization: Basic dXNlcjpwYXNz",
			want:  "Authorization: Basic [REDACTED]",
		},
		{
			name:  "short bearer token with digits",
			input: "Authorization: Bearer mytoken123",
			want:  "Authorization: Bearer [REDACTED]",
		},
		{
			name:  "short lowercase bearer token",
			input: "Authorization: Bearer mysecret",
			want:  "Authorization: Bearer [REDACTED]",
		},
		{
			name:  "very short bearer token",
			input: "Authorization: Bearer abc123",
			want:  "Authorization: Bearer [REDACTED]",
		},
		{
			name:  "lowercase bearer token",
			input: "Authorization: Bearer abcdefghijklmnopqrstuvwxyz",
			want:  "Authorization: Bearer [REDACTED]",
		},
		{
			name:  "env password",
			input: "password=hunter2",
			want:  "password=[REDACTED]",
		},
		{
			name:  "weak password literal",
			input: "password=secret",
			want:  "password=[REDACTED]",
		},
		{
			name:  "password with comma",
			input: "password=p@ss,word",
			want:  "password=[REDACTED]",
		},
		{
			name:  "unquoted multiline assignment",
			input: "password=hunter2 token=abc123",
			want:  "password=[REDACTED] token=[REDACTED]",
		},
		{
			name:  "unquoted env passphrase",
			input: "PASSWORD=correct horse battery staple",
			want:  "PASSWORD=[REDACTED]",
		},
		{
			name:  "assignment followed by prose",
			input: "Set token=abc123 and restart the service",
			want:  "Set token=[REDACTED] and restart the service",
		},
		{
			name:  "short env pass key",
			input: "DB_PASS=hunter2",
			want:  "DB_PASS=[REDACTED]",
		},
		{
			name:  "all caps password",
			input: "DB_PASSWORD=SUPERSECRET",
			want:  "DB_PASSWORD=[REDACTED]",
		},
		{
			name:  "all caps api key",
			input: "API_KEY=ABC123",
			want:  "API_KEY=[REDACTED]",
		},
		{
			name:  "cli password flag",
			input: "deploy --password hunter2 --verbose",
			want:  "deploy --password [REDACTED] --verbose",
		},
		{
			name:  "quoted config line",
			input: `"password: hunter2"`,
			want:  `"password: [REDACTED]"`,
		},
		{
			name:  "yaml password",
			input: "password: hunter2",
			want:  "password: [REDACTED]",
		},
		{
			name:  "json password",
			input: `{"password": "s3cr3t!"}`,
			want:  `{"password": "[REDACTED]"}`,
		},
		{
			name:  "camel case quoted password",
			input: `password = "mySecret"`,
			want:  `password = "[REDACTED]"`,
		},
		{
			name:  "env camel case quoted password",
			input: `DB_PASSWORD="correctHorseBatteryStaple"`,
			want:  `DB_PASSWORD="[REDACTED]"`,
		},
		{
			name:  "quoted password with spaces",
			input: "DB_PASSWORD='correct horse battery staple'",
			want:  "DB_PASSWORD='[REDACTED]'",
		},
		{
			name:  "go assignment",
			input: `password := "hunter2"`,
			want:  `password := "[REDACTED]"`,
		},
		{
			name:  "client secret",
			input: "client_secret=abcd1234",
			want:  "client_secret=[REDACTED]",
		},
		{
			name:  "aws secret access key",
			input: "export AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			want:  "export AWS_SECRET_ACCESS_KEY=[REDACTED]",
		},
		{
			name:  "api key header",
			input: "X-Api-Key: abc123def456",
			want:  "X-Api-Key: [REDACTED]",
		},
		{
			name:  "generic token",
			input: "token=abc123",
			want:  "token=[REDACTED]",
		},
		{
			name:  "database url password",
			input: "DATABASE_URL=postgres://app:supersecret@db:5432/name",
			want:  "DATABASE_URL=postgres://app:[REDACTED]@db:5432/name",
		},
		{
			name:  "url password without user",
			input: "redis://:supersecret@cache:6379",
			want:  "redis://:[REDACTED]@cache:6379",
		},
		{
			name:  "rsa private key",
			input: "before\n-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA\n-----END RSA PRIVATE KEY-----\nafter",
			want:  "before\n[REDACTED]\nafter",
		},
		{
			name:  "openssh private key",
			input: "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAA\n-----END OPENSSH PRIVATE KEY-----",
			want:  "[REDACTED]",
		},
		{
			name:  "unterminated private key",
			input: "before\n-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASC",
			want:  "before\n[REDACTED]",
		},
		{
			name:  "private key header after prose",
			input: "Here is the key: -----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASC",
			want:  "Here is the key: [REDACTED]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mask.Text(tt.input)
			if got != tt.want {
				t.Errorf("Text() = %q, want %q", got, tt.want)
			}
			if again := mask.Text(got); again != got {
				t.Errorf("Text() is not idempotent: second pass = %q, want %q", again, got)
			}
		})
	}
}

func TestTextLeavesOrdinaryTextAndCodeUntouched(t *testing.T) {
	inputs := []string{
		"The password reset flow sends an email. Use the token from the message.",
		"Bearer authentication is required for this endpoint.",
		"Basic authentication is not supported.",
		"Rotate the API key in the dashboard.",
		"func login(password string) error {\n\tif password == \"\" {\n\t\treturn errors.New(\"password is required\")\n\t}\n\treturn nil\n}",
		"token := findToken(ctx)",
		"passwordHash := hashPassword(password)",
		"password = user.Password",
		"password = os.Getenv(\"DB_PASSWORD\")",
		"password = \"${DB_PASSWORD}\"",
		"password = \"required\"",
		"token := \"\"",
		"authToken := \"getToken\"",
		"token := \"getAccessToken\"",
		"compass = \"north\"",
		"passenger = \"train\"",
		"see -----BEGIN PRIVATE KEY----- in the docs",
		"const apiKeyHeader = \"X-Api-Key\"",
		"token_endpoint: https://login.example.com/oauth2/token",
		"api_key_header: X-Api-Key",
		"token_type = \"access\"",
		"password_policy = \"required\"",
		"secret_name = \"my-app-secret\"",
		"// password: minimum length is 8",
		"password: choose a strong one",
		"token: expires after one hour",
		"secretary and tokenizer are ordinary words",
		"SELECT id, password_hash FROM users WHERE token = ?",
		"проверка пароля и токена в обычном тексте",
	}

	for _, input := range inputs {
		if got := mask.Text(input); got != input {
			t.Errorf("Text() changed ordinary text\ninput: %q\n got:  %q", input, got)
		}
	}
}

func TestTextKeepsSurroundingProseIntact(t *testing.T) {
	input := "Use sk-proj-AbCdEf0123456789GhIjKlMnOpQrStUv to call the API, then rotate it."
	got := mask.Text(input)
	if strings.Contains(got, "sk-proj") {
		t.Fatalf("Text() = %q, want the provider key removed", got)
	}
	if !strings.HasPrefix(got, "Use ") || !strings.HasSuffix(got, " to call the API, then rotate it.") {
		t.Errorf("Text() = %q, want surrounding prose preserved", got)
	}
}
