package cli

import (
	"io"
	"strings"
	"testing"

	"github.com/patent-dev/bulk-file-loader/api/generated"
)

// epoFields are the EPO BDDS adapter's credential fields.
var epoFields = []generated.CredentialField{
	{Key: "username", Label: "Username", Type: generated.Text, Required: true},
	{Key: "password", Label: "Password", Type: generated.Password, Required: true},
}

func testInput(env map[string]string, tty bool, prompted map[string]string) credentialInput {
	return credentialInput{
		stdin:   strings.NewReader(""),
		isTTY:   tty,
		getenv:  func(k string) string { return env[k] },
		prompt:  func(label string, _ bool) (string, error) { return prompted[label], nil },
		warnOut: io.Discard,
	}
}

func TestCompleteCredentialsFromEnvironment(t *testing.T) {
	in := testInput(map[string]string{"EPO_BDDS_USERNAME": "user@example.com", "EPO_BDDS_PASSWORD": "secret"}, false, nil)
	creds := map[string]string{}
	if err := completeCredentials(in, "epo-bdds", epoFields, creds); err != nil {
		t.Fatal(err)
	}
	if creds["username"] != "user@example.com" || creds["password"] != "secret" {
		t.Errorf("creds = %v, want both from the environment", creds)
	}
}

func TestCompleteCredentialsFlagWinsOverEnvironment(t *testing.T) {
	in := testInput(map[string]string{"EPO_BDDS_USERNAME": "env@example.com", "EPO_BDDS_PASSWORD": "secret"}, false, nil)
	creds := map[string]string{"username": "flag@example.com"}
	if err := completeCredentials(in, "epo-bdds", epoFields, creds); err != nil {
		t.Fatal(err)
	}
	if creds["username"] != "flag@example.com" {
		t.Errorf("username = %q, want the flag value", creds["username"])
	}
}

func TestCompleteCredentialsPromptsOnTerminal(t *testing.T) {
	in := testInput(nil, true, map[string]string{"Username": "user@example.com", "Password": "secret"})
	creds := map[string]string{}
	if err := completeCredentials(in, "epo-bdds", epoFields, creds); err != nil {
		t.Fatal(err)
	}
	if creds["password"] != "secret" {
		t.Errorf("creds = %v, want the prompted values", creds)
	}
}

func TestCompleteCredentialsWithoutTerminalNamesTheOptions(t *testing.T) {
	in := testInput(nil, false, nil)
	creds := map[string]string{"username": "user@example.com"}
	err := completeCredentials(in, "epo-bdds", epoFields, creds)
	if err == nil {
		t.Fatal("a missing password without a terminal must fail")
	}
	for _, want := range []string{"--password-stdin", "$EPO_BDDS_PASSWORD"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
}

func TestReadSecretStripsNewline(t *testing.T) {
	for _, in := range []string{"s3cret\n", "s3cret\r\n", "s3cret"} {
		got, err := readSecret(strings.NewReader(in))
		if err != nil || got != "s3cret" {
			t.Errorf("readSecret(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := readSecret(strings.NewReader("\n")); err == nil {
		t.Error("an empty secret must be rejected")
	}
}

func TestCredentialsFromFlagsReadsPasswordFromStdin(t *testing.T) {
	t.Cleanup(func() { sourceUsername, sourcePasswordStdin = "", false })
	sourceUsername, sourcePasswordStdin = "user@example.com", true
	in := testInput(nil, false, nil)
	in.stdin = strings.NewReader("secret\n")
	creds, err := credentialsFromFlags(in)
	if err != nil {
		t.Fatal(err)
	}
	if creds["username"] != "user@example.com" || creds["password"] != "secret" {
		t.Errorf("creds = %v", creds)
	}
}

func TestCredentialsGivenIncludesEnvironment(t *testing.T) {
	in := testInput(map[string]string{"EPO_BDDS_PASSWORD": "from-env"}, false, nil)
	creds, err := credentialsGiven(in, "epo-bdds")
	if err != nil {
		t.Fatal(err)
	}
	if creds["password"] != "from-env" {
		t.Errorf("creds = %v, want the password from the environment", creds)
	}
	if creds, _ := credentialsGiven(testInput(nil, false, nil), "epo-bdds"); len(creds) != 0 {
		t.Errorf("creds = %v, want none given", creds)
	}
}
