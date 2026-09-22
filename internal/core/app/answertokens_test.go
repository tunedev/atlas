package app_test

import (
	"strings"
	"testing"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

// toks builds a completion whose text is the concatenation of the given token
// texts, which is how a real completion arrives.
func toks(texts ...string) ports.Completion {
	c := ports.Completion{}
	for _, t := range texts {
		c.Text += t
		c.Tokens = append(c.Tokens, ports.Token{Text: t, LogProb: -0.1})
	}
	return c
}

func TestEachFieldGetsItsOwnAnswerToken(t *testing.T) {
	c := toks(`{"`, `readable`, `":`, ` "`, `yes`, `",`, ` "`, `genre`, `":`, ` "`, `poetry`, `"}`)

	got, err := app.AnswerTokens(c, []string{"readable", "genre"})
	if err != nil {
		t.Fatalf("answer tokens: %v", err)
	}
	if got["readable"].Text != "yes" {
		t.Errorf("readable answer token = %q, want yes", got["readable"].Text)
	}
	if got["genre"].Text != "poetry" {
		t.Errorf("genre answer token = %q, want poetry", got["genre"].Text)
	}
}

func TestTheFirstTokenOfAMultiTokenValueIsTheAnswerToken(t *testing.T) {
	// "medium" arrives as two tokens. The distinction between options is made
	// at the first of them.
	c := toks(`{"`, `length`, `":`, ` "`, `med`, `ium`, `"}`)

	got, err := app.AnswerTokens(c, []string{"length"})
	if err != nil {
		t.Fatalf("answer tokens: %v", err)
	}
	if got["length"].Text != "med" {
		t.Errorf("answer token = %q, want med; a later token cannot distinguish options that share a prefix", got["length"].Text)
	}
}

func TestFieldsAnsweredOutOfOrderAreStillKeyedCorrectly(t *testing.T) {
	c := toks(`{"`, `genre`, `":`, ` "`, `history`, `",`, ` "`, `readable`, `":`, ` "`, `no`, `"}`)

	got, err := app.AnswerTokens(c, []string{"readable", "genre"})
	if err != nil {
		t.Fatalf("answer tokens: %v", err)
	}
	if got["readable"].Text != "no" || got["genre"].Text != "history" {
		t.Errorf("answers are positional rather than keyed: %+v", map[string]string{
			"readable": got["readable"].Text, "genre": got["genre"].Text,
		})
	}
}

func TestAMissingFieldIsAnErrorNamingIt(t *testing.T) {
	c := toks(`{"`, `readable`, `":`, ` "`, `yes`, `"}`)

	_, err := app.AnswerTokens(c, []string{"readable", "genre"})
	if err == nil {
		t.Fatal("a question the model never answered produced no error")
	}
	if !strings.Contains(err.Error(), "genre") {
		t.Errorf("error does not name the missing question: %v", err)
	}
}

func TestAnswerTokensOnACompletionWithNoTokensIsAnError(t *testing.T) {
	c := ports.Completion{Text: `{"readable": "yes"}`}
	if _, err := app.AnswerTokens(c, []string{"readable"}); err == nil {
		t.Fatal("a completion carrying no per-token data returned no error")
	}
}

func TestATokenStreamThatIsNotJSONIsAnError(t *testing.T) {
	c := toks(`I `, `think `, `yes`)
	if _, err := app.AnswerTokens(c, []string{"readable"}); err == nil {
		t.Fatal("prose was accepted as an answer object")
	}
}
