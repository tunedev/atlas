package app_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

type recordingProvider struct {
	completion ports.Completion
	calls      int
	last       ports.Prompt
}

func (p *recordingProvider) Name() string { return "recording" }

func (p *recordingProvider) Complete(_ context.Context, pr ports.Prompt) (ports.Completion, error) {
	p.calls++
	p.last = pr
	return p.completion, nil
}

// failingProvider is a ports.Provider that always returns a fixed error.
type failingProvider struct{}

func (p *failingProvider) Name() string { return "failing" }

func (p *failingProvider) Complete(_ context.Context, _ ports.Prompt) (ports.Completion, error) {
	return ports.Completion{}, errors.New("provider down")
}

// twoAnswers is a completion answering two questions, with alternatives that
// split the same answer across surface forms.
func twoAnswers() ports.Completion {
	tok := func(text string, alts ...ports.Alternative) ports.Token {
		return ports.Token{Text: text, LogProb: math.Log(0.2), Alternatives: alts}
	}
	plain := func(text string) ports.Token { return ports.Token{Text: text, LogProb: math.Log(0.9)} }

	c := ports.Completion{Model: "a-model"}
	add := func(t ports.Token) { c.Text += t.Text; c.Tokens = append(c.Tokens, t) }

	add(plain(`{"`))
	add(plain(`readable`))
	add(plain(`":`))
	add(plain(` "`))
	add(tok(`yes`,
		ports.Alternative{Text: "Yes", LogProb: math.Log(0.70)},
		ports.Alternative{Text: "yes", LogProb: math.Log(0.25)},
		ports.Alternative{Text: "no", LogProb: math.Log(0.05)}))
	add(plain(`",`))
	add(plain(` "`))
	add(plain(`length`))
	add(plain(`":`))
	add(plain(` "`))
	add(tok(`long`,
		ports.Alternative{Text: "long", LogProb: math.Log(0.60)},
		ports.Alternative{Text: "medium", LogProb: math.Log(0.30)},
		ports.Alternative{Text: "short", LogProb: math.Log(0.10)}))
	add(plain(`"}`))
	return c
}

func judgeQuestions() []ports.Question {
	return []ports.Question{
		{ID: "readable", Kind: ports.KindNoul, Ask: "Is it readable?"},
		{ID: "length", Kind: ports.KindScore, Ask: "How long is it?", Options: []string{"short", "medium", "long"}},
	}
}

func testJudgeConfig() app.JudgeConfig {
	return app.JudgeConfig{Temperature: 0, Seed: 7, TopLogProbs: 5, MaxTokens: 128}
}

func TestEveryQuestionIsAnsweredInOneCall(t *testing.T) {
	p := &recordingProvider{completion: twoAnswers()}

	got, err := app.NewJudge(p, testJudgeConfig()).Ask(context.Background(), "a short book", judgeQuestions())
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	if p.calls != 1 {
		t.Errorf("provider called %d times, want 1; questions asked separately invite contradictions", p.calls)
	}
	if len(got.Answers) != 2 {
		t.Fatalf("answers = %d, want 2", len(got.Answers))
	}
	if got.Model != "a-model" {
		t.Errorf("judgement does not record which model answered: %q", got.Model)
	}
}

func TestANoulCarriesSummedMassNotTheEmittedTokensProbability(t *testing.T) {
	p := &recordingProvider{completion: twoAnswers()}
	got, err := app.NewJudge(p, testJudgeConfig()).Ask(context.Background(), "a short book", judgeQuestions())
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	var readable ports.Answer
	for _, a := range got.Answers {
		if a.ID == "readable" {
			readable = a
		}
	}
	if readable.Distribution["yes"] < 0.90 {
		t.Errorf("yes = %.4f, want at least 0.90; Yes and yes are the same answer", readable.Distribution["yes"])
	}
	if math.Abs(readable.Distribution["yes"]-0.25) < 0.01 {
		t.Error("the emitted token's own probability was reported instead of the summed mass")
	}
	if readable.Chosen != "yes" {
		t.Errorf("chosen = %q, want yes", readable.Chosen)
	}
}

func TestAScoreCarriesAProbabilityWeightedPosition(t *testing.T) {
	p := &recordingProvider{completion: twoAnswers()}
	got, err := app.NewJudge(p, testJudgeConfig()).Ask(context.Background(), "a short book", judgeQuestions())
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	var length ports.Answer
	for _, a := range got.Answers {
		if a.ID == "length" {
			length = a
		}
	}
	// short=0, medium=1, long=2 weighted by 0.10, 0.30, 0.60.
	want := 0*0.10 + 1*0.30 + 2*0.60
	if math.Abs(length.Expected-want) > 0.01 {
		t.Errorf("expected = %.4f, want %.4f", length.Expected, want)
	}
	if length.Chosen != "long" {
		t.Errorf("chosen = %q, want long", length.Chosen)
	}
}

func TestTheRequestCarriesTheSchemaLogprobsAndPinnedSampling(t *testing.T) {
	p := &recordingProvider{completion: twoAnswers()}
	if _, err := app.NewJudge(p, testJudgeConfig()).Ask(context.Background(), "a short book", judgeQuestions()); err != nil {
		t.Fatalf("ask: %v", err)
	}
	if len(p.last.Schema) == 0 || !strings.Contains(string(p.last.Schema), "enum") {
		t.Errorf("no enum schema was sent; a choice could answer anything: %s", p.last.Schema)
	}
	if p.last.TopLogProbs < 2 {
		t.Errorf("TopLogProbs = %d; without alternatives there is no mass to sum", p.last.TopLogProbs)
	}
	if p.last.Temperature == nil || *p.last.Temperature != 0 {
		t.Errorf("temperature = %v, want a pinned 0", p.last.Temperature)
	}
	if p.last.Seed == nil || *p.last.Seed != 7 {
		t.Errorf("seed = %v, want the configured 7", p.last.Seed)
	}
	if !strings.Contains(p.last.User, "a short book") {
		t.Error("the subject did not reach the prompt")
	}
	for _, q := range judgeQuestions() {
		if !strings.Contains(p.last.User, q.Ask) {
			t.Errorf("question %q did not reach the prompt", q.ID)
		}
	}
}

func TestAnAnswerTokenWithoutAlternativesIsAnErrorNamingTheQuestion(t *testing.T) {
	c := twoAnswers()
	for i := range c.Tokens {
		c.Tokens[i].Alternatives = nil
	}
	p := &recordingProvider{completion: c}

	_, err := app.NewJudge(p, testJudgeConfig()).Ask(context.Background(), "a short book", judgeQuestions())
	if err == nil {
		t.Fatal("a completion with no alternatives produced a distribution; that number would be invented")
	}
	if !strings.Contains(err.Error(), "readable") {
		t.Errorf("error does not name the question: %v", err)
	}
}

// TestAnAnswerCarriesTheAlternativesItWasReadFrom covers F2: the raw
// alternatives at an answer token travel with the answer, so a distribution
// built from one alternative can later be told apart from one built from
// several.
func TestAnAnswerCarriesTheAlternativesItWasReadFrom(t *testing.T) {
	p := &recordingProvider{completion: twoAnswers()}
	got, err := app.NewJudge(p, testJudgeConfig()).Ask(context.Background(), "a short book", judgeQuestions())
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	var readable ports.Answer
	for _, a := range got.Answers {
		if a.ID == "readable" {
			readable = a
		}
	}
	if len(readable.Alternatives) != 3 {
		t.Fatalf("alternatives = %d, want 3 (as returned at the answer token)", len(readable.Alternatives))
	}
	texts := map[string]bool{}
	for _, alt := range readable.Alternatives {
		texts[alt.Text] = true
	}
	for _, want := range []string{"Yes", "yes", "no"} {
		if !texts[want] {
			t.Errorf("alternatives = %v, missing %q", readable.Alternatives, want)
		}
	}
}

// TestTheJudgementRecordsItsProviderAndSampling covers F3: a probability
// read under unknown sampling is not calibrated, so the provider name and
// the sampling that produced it travel with the judgement.
func TestTheJudgementRecordsItsProviderAndSampling(t *testing.T) {
	p := &recordingProvider{completion: twoAnswers()}
	cfg := testJudgeConfig()
	got, err := app.NewJudge(p, cfg).Ask(context.Background(), "a short book", judgeQuestions())
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	if got.Provider != "recording" {
		t.Errorf("provider = %q, want recording", got.Provider)
	}
	if got.Sampling.Temperature != cfg.Temperature || got.Sampling.Seed != cfg.Seed ||
		got.Sampling.TopLogProbs != cfg.TopLogProbs || got.Sampling.MaxTokens != cfg.MaxTokens {
		t.Errorf("sampling = %+v, want %+v", got.Sampling, cfg)
	}
}

func TestAProviderFailureIsReturnedNotSwallowed(t *testing.T) {
	p := &failingProvider{}
	if _, err := app.NewJudge(p, testJudgeConfig()).Ask(context.Background(), "a short book", judgeQuestions()); err == nil {
		t.Fatal("a failing provider produced a judgement")
	}
}

// splitOptionAnswer answers a single "focus" choice question whose chosen
// option, "backend", is tokenised as "back" then "end" -- the answer token's
// own text is only the option's first token, so matching it against whole
// option strings fails even though the option is unambiguous.
func splitOptionAnswer() ports.Completion {
	tok := func(text string, alts ...ports.Alternative) ports.Token {
		return ports.Token{Text: text, LogProb: math.Log(0.5), Alternatives: alts}
	}
	plain := func(text string) ports.Token { return ports.Token{Text: text, LogProb: math.Log(0.9)} }

	c := ports.Completion{Model: "a-model"}
	add := func(t ports.Token) { c.Text += t.Text; c.Tokens = append(c.Tokens, t) }

	add(plain(`{"`))
	add(plain(`focus`))
	add(plain(`":`))
	add(plain(` "`))
	add(tok(`back`,
		ports.Alternative{Text: "back", LogProb: math.Log(0.5)},
		ports.Alternative{Text: "front", LogProb: math.Log(0.3)},
		ports.Alternative{Text: "data", LogProb: math.Log(0.2)}))
	add(plain(`end`))
	add(plain(`"}`))
	return c
}

func focusQuestion() ports.Question {
	return ports.Question{
		ID:      "focus",
		Kind:    ports.KindChoice,
		Ask:     "What is the main focus?",
		Options: []string{"backend", "frontend", "platform", "data", "other"},
	}
}

func TestAnAnswerResolvesByTheFirstTokenOfAMultiTokenOption(t *testing.T) {
	p := &recordingProvider{completion: splitOptionAnswer()}

	got, err := app.NewJudge(p, testJudgeConfig()).Ask(context.Background(), "a short book", []ports.Question{focusQuestion()})
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	if len(got.Answers) != 1 {
		t.Fatalf("answers = %d, want 1", len(got.Answers))
	}
	focus := got.Answers[0]
	if focus.Chosen != "backend" {
		t.Errorf("chosen = %q, want backend; \"back\" is a prefix of only one option", focus.Chosen)
	}
	if focus.Distribution["backend"] < 0.4 {
		t.Errorf("backend mass = %.4f, want at least 0.4", focus.Distribution["backend"])
	}
}

// ambiguousPrefixAnswer answers a "shape" choice question whose answer token
// text, "pla", is a first-token prefix of two different options: platform
// and plain. Neither can be preferred, so the read must fail rather than
// guess.
func ambiguousPrefixAnswer() ports.Completion {
	plain := func(text string) ports.Token { return ports.Token{Text: text, LogProb: math.Log(0.9)} }
	c := ports.Completion{Model: "a-model"}
	add := func(t ports.Token) { c.Text += t.Text; c.Tokens = append(c.Tokens, t) }

	add(plain(`{"`))
	add(plain(`shape`))
	add(plain(`":`))
	add(plain(` "`))
	add(ports.Token{Text: "pla", LogProb: math.Log(0.9), Alternatives: []ports.Alternative{
		{Text: "pla", LogProb: math.Log(0.9)},
		{Text: "other", LogProb: math.Log(0.1)},
	}})
	add(plain(`in`))
	add(plain(`"}`))
	return c
}

func shapeQuestion() ports.Question {
	return ports.Question{
		ID:      "shape",
		Kind:    ports.KindChoice,
		Ask:     "What shape is it?",
		Options: []string{"platform", "plain", "other"},
	}
}

func TestATokenPrefixingTwoDifferentOptionsIsAnAmbiguityError(t *testing.T) {
	p := &recordingProvider{completion: ambiguousPrefixAnswer()}

	_, err := app.NewJudge(p, testJudgeConfig()).Ask(context.Background(), "a short book", []ports.Question{shapeQuestion()})
	if err == nil {
		t.Fatal("an alternative prefixing two different options produced a judgement instead of an error")
	}
	if !strings.Contains(err.Error(), "shape") {
		t.Errorf("error does not name the question: %v", err)
	}
	if !strings.Contains(err.Error(), "pla") {
		t.Errorf("error does not name the ambiguous text: %v", err)
	}
}
