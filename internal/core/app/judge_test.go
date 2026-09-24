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

// quoteGluedAnswer answers "readable" where the opening quote is glued to
// both the answer token and its alternatives (`"yes`, `"Yes`, `"no`), rather
// than arriving as a separate punctuation-only token -- the shape F6 flags
// as untested: every other fixture in this package splits the quote off.
func quoteGluedAnswer() ports.Completion {
	tok := func(text string, alts ...ports.Alternative) ports.Token {
		return ports.Token{Text: text, LogProb: math.Log(0.2), Alternatives: alts}
	}
	plain := func(text string) ports.Token { return ports.Token{Text: text, LogProb: math.Log(0.9)} }

	c := ports.Completion{Model: "a-model"}
	add := func(t ports.Token) { c.Text += t.Text; c.Tokens = append(c.Tokens, t) }

	add(plain(`{"`))
	add(plain(`readable`))
	add(plain(`":`))
	add(tok(`"yes`,
		ports.Alternative{Text: `"Yes`, LogProb: math.Log(0.70)},
		ports.Alternative{Text: `"yes`, LogProb: math.Log(0.25)},
		ports.Alternative{Text: `"no`, LogProb: math.Log(0.05)}))
	add(plain(`"}`))
	return c
}

func TestAQuoteGluedAnswerTokenStillResolves(t *testing.T) {
	p := &recordingProvider{completion: quoteGluedAnswer()}

	got, err := app.NewJudge(p, testJudgeConfig()).Ask(context.Background(), "a short book",
		[]ports.Question{{ID: "readable", Kind: ports.KindNoul, Ask: "Is it readable?"}})
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	readable := got.Answers[0]
	if readable.Chosen != "yes" {
		t.Errorf("chosen = %q, want yes; a quote glued to the value must still be trimmed before matching", readable.Chosen)
	}
	if readable.Distribution["yes"] < 0.90 {
		t.Errorf("yes = %.4f, want at least 0.90", readable.Distribution["yes"])
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
	// F5: the error must be diagnosable rather than merely present -- it
	// says whether the ambiguous text was an alternative or the emitted
	// token, and carries its probability.
	if !strings.Contains(err.Error(), "alternative") {
		t.Errorf("error does not say the ambiguous text was an alternative: %v", err)
	}
	if !strings.Contains(err.Error(), "p=0.9") {
		t.Errorf("error does not carry the ambiguous text's probability: %v", err)
	}
}

// ambiguousOwnTextAnswer answers "shape" with an emitted token, "pla", that
// is itself a first-token prefix of two options -- and neither of its own
// alternatives repeats that text, so the ambiguity is found in the fallback
// read of the emitted token, not in the alternatives loop.
func ambiguousOwnTextAnswer() ports.Completion {
	plain := func(text string) ports.Token { return ports.Token{Text: text, LogProb: math.Log(0.9)} }
	c := ports.Completion{Model: "a-model"}
	add := func(t ports.Token) { c.Text += t.Text; c.Tokens = append(c.Tokens, t) }

	add(plain(`{"`))
	add(plain(`shape`))
	add(plain(`":`))
	add(plain(` "`))
	add(ports.Token{Text: "pla", LogProb: math.Log(0.85), Alternatives: []ports.Alternative{
		{Text: "xyz", LogProb: math.Log(0.85)},
		{Text: "abc", LogProb: math.Log(0.15)},
	}})
	add(plain(`in`))
	add(plain(`"}`))
	return c
}

func TestAnAmbiguousEmittedTokenIsDistinguishedFromAnAmbiguousAlternative(t *testing.T) {
	p := &recordingProvider{completion: ambiguousOwnTextAnswer()}

	_, err := app.NewJudge(p, testJudgeConfig()).Ask(context.Background(), "a short book", []ports.Question{shapeQuestion()})
	if err == nil {
		t.Fatal("an emitted token prefixing two different options produced a judgement instead of an error")
	}
	if !strings.Contains(err.Error(), "emitted token") {
		t.Errorf("error does not say the ambiguous text was the emitted token: %v", err)
	}
	if !strings.Contains(err.Error(), "p=0.85") {
		t.Errorf("error does not carry the emitted token's probability: %v", err)
	}
}

// tossQuestion is a two-option choice, the smallest declared option set the
// confidence formula is defined for.
func tossQuestion() ports.Question {
	return ports.Question{ID: "toss", Kind: ports.KindChoice, Ask: "a or b?", Options: []string{"a", "b"}}
}

// evenAcrossDeclaredAnswer answers "toss" with mass split evenly between its
// two declared options, the maximum-entropy case for two options.
func evenAcrossDeclaredAnswer() ports.Completion {
	tok := func(text string, alts ...ports.Alternative) ports.Token {
		return ports.Token{Text: text, LogProb: math.Log(0.5), Alternatives: alts}
	}
	plain := func(text string) ports.Token { return ports.Token{Text: text, LogProb: math.Log(0.9)} }
	c := ports.Completion{Model: "a-model"}
	add := func(t ports.Token) { c.Text += t.Text; c.Tokens = append(c.Tokens, t) }

	add(plain(`{"`))
	add(plain(`toss`))
	add(plain(`":`))
	add(plain(` "`))
	add(tok(`a`,
		ports.Alternative{Text: "a", LogProb: math.Log(0.5)},
		ports.Alternative{Text: "b", LogProb: math.Log(0.5)}))
	add(plain(`"}`))
	return c
}

func TestConfidenceIsZeroForAnEvenDistributionAcrossDeclaredOptions(t *testing.T) {
	p := &recordingProvider{completion: evenAcrossDeclaredAnswer()}
	got, err := app.NewJudge(p, testJudgeConfig()).Ask(context.Background(), "a short book", []ports.Question{tossQuestion()})
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	toss := got.Answers[0]
	if math.Abs(toss.Confidence) > 0.001 {
		t.Errorf("confidence = %.4f, want 0 for an even split across both declared options", toss.Confidence)
	}
}

// threeWayQuestion is a three-option choice used to tell a genuinely
// measured 1.0 (mass concentrated on one option because that option is the
// only one the alternatives named) apart from a manufactured one.
func threeWayQuestion() ports.Question {
	return ports.Question{ID: "pick", Kind: ports.KindChoice, Ask: "x, y or z?", Options: []string{"x", "y", "z"}}
}

// oneAlternativeAnswer answers "pick" where the only alternative naming a
// declared option is "x" itself; "q" is noise that matches nothing. The mass
// on x comes from an alternative, not the own-text fallback.
func oneAlternativeAnswer() ports.Completion {
	tok := func(text string, alts ...ports.Alternative) ports.Token {
		return ports.Token{Text: text, LogProb: math.Log(0.9), Alternatives: alts}
	}
	plain := func(text string) ports.Token { return ports.Token{Text: text, LogProb: math.Log(0.9)} }
	c := ports.Completion{Model: "a-model"}
	add := func(t ports.Token) { c.Text += t.Text; c.Tokens = append(c.Tokens, t) }

	add(plain(`{"`))
	add(plain(`pick`))
	add(plain(`":`))
	add(plain(` "`))
	add(tok(`x`,
		ports.Alternative{Text: "x", LogProb: math.Log(0.9)},
		ports.Alternative{Text: "q", LogProb: math.Log(0.1)}))
	add(plain(`"}`))
	return c
}

func TestConfidenceIsOneWhenAllMatchedMassSitsOnOneOption(t *testing.T) {
	p := &recordingProvider{completion: oneAlternativeAnswer()}
	got, err := app.NewJudge(p, testJudgeConfig()).Ask(context.Background(), "a short book", []ports.Question{threeWayQuestion()})
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	pick := got.Answers[0]
	if math.Abs(pick.Confidence-1) > 0.001 {
		t.Errorf("confidence = %.4f, want 1 when all matched mass sits on one option", pick.Confidence)
	}
	if pick.Coverage.Represented != 1 {
		t.Errorf("represented = %d, want 1; \"x\" is the only alternative naming a declared option", pick.Coverage.Represented)
	}
	if pick.Coverage.Declared != 3 {
		t.Errorf("declared = %d, want 3", pick.Coverage.Declared)
	}
}

// noOptionInAlternativesQuestion mirrors the live "focus" question that
// motivated this change: five declared options, none of which the engine's
// alternatives named.
func noOptionInAlternativesQuestion() ports.Question {
	return ports.Question{
		ID:      "focus",
		Kind:    ports.KindChoice,
		Ask:     "What is the main focus?",
		Options: []string{"backend", "frontend", "platform", "data", "other"},
	}
}

// noOptionInAlternativesAnswer answers "focus" with "other", a real declared
// option, but every alternative at that token is prose the model would have
// written before the schema forced compliance -- none of them is "other" or
// any other declared option. The own-text fallback is the only reason
// "other" enters the distribution at all.
func noOptionInAlternativesAnswer() ports.Completion {
	tok := func(text string, alts ...ports.Alternative) ports.Token {
		return ports.Token{Text: text, LogProb: math.Log(0.883), Alternatives: alts}
	}
	plain := func(text string) ports.Token { return ports.Token{Text: text, LogProb: math.Log(0.9)} }
	c := ports.Completion{Model: "a-model"}
	add := func(t ports.Token) { c.Text += t.Text; c.Tokens = append(c.Tokens, t) }

	add(plain(`{"`))
	add(plain(`focus`))
	add(plain(`":`))
	add(plain(` "`))
	add(tok(`other`,
		ports.Alternative{Text: "AI", LogProb: math.Log(0.883)},
		ports.Alternative{Text: "Building", LogProb: math.Log(0.05)},
		ports.Alternative{Text: "Develop", LogProb: math.Log(0.03)},
		ports.Alternative{Text: "Internal", LogProb: math.Log(0.02)},
		ports.Alternative{Text: "Business", LogProb: math.Log(0.017)}))
	add(plain(`"}`))
	return c
}

// TestRepresentedIsZeroWhenNoAlternativeNamesADeclaredOption is the finding
// this change exists to surface: the own-text fallback still yields Chosen
// "other" and a Distribution of exactly {other: 1}, and by the entropy
// formula alone that reads as maximum Confidence -- but Represented is 0,
// because none of the alternatives said anything about any declared option.
// A confident-looking answer and a zero-coverage one are the same answer.
func TestRepresentedIsZeroWhenNoAlternativeNamesADeclaredOption(t *testing.T) {
	p := &recordingProvider{completion: noOptionInAlternativesAnswer()}
	got, err := app.NewJudge(p, testJudgeConfig()).Ask(context.Background(), "a short book", []ports.Question{noOptionInAlternativesQuestion()})
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	focus := got.Answers[0]
	if focus.Chosen != "other" {
		t.Fatalf("chosen = %q, want other", focus.Chosen)
	}
	if math.Abs(focus.Distribution["other"]-1) > 0.001 {
		t.Errorf("distribution[other] = %.4f, want 1 (the own-text fallback is a single-member map)", focus.Distribution["other"])
	}
	if math.Abs(focus.Confidence-1) > 0.001 {
		t.Errorf("confidence = %.4f, want 1; the entropy formula alone cannot tell this apart from a measured 1.0", focus.Confidence)
	}
	if focus.Coverage.Represented != 0 {
		t.Errorf("represented = %d, want 0; none of the alternatives named a declared option", focus.Coverage.Represented)
	}
	if focus.Coverage.Declared != 5 {
		t.Errorf("declared = %d, want 5", focus.Coverage.Declared)
	}
}

// partialCoverageAnswer answers "focus" where the alternatives name exactly
// two of its five declared options (backend, frontend), plus noise that
// matches neither.
func partialCoverageAnswer() ports.Completion {
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
	add(tok(`backend`,
		ports.Alternative{Text: "backend", LogProb: math.Log(0.5)},
		ports.Alternative{Text: "frontend", LogProb: math.Log(0.3)},
		ports.Alternative{Text: "AI", LogProb: math.Log(0.1)},
		ports.Alternative{Text: "Building", LogProb: math.Log(0.1)}))
	add(plain(`"}`))
	return c
}

func TestCoverageReportsPartialRepresentation(t *testing.T) {
	p := &recordingProvider{completion: partialCoverageAnswer()}
	got, err := app.NewJudge(p, testJudgeConfig()).Ask(context.Background(), "a short book", []ports.Question{noOptionInAlternativesQuestion()})
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	focus := got.Answers[0]
	if focus.Coverage.Represented != 2 {
		t.Errorf("represented = %d, want 2 (backend, frontend)", focus.Coverage.Represented)
	}
	if focus.Coverage.Declared != 5 {
		t.Errorf("declared = %d, want 5", focus.Coverage.Declared)
	}
}

// fullCoverageAnswer answers "focus" where the alternatives name all five of
// its declared options.
func fullCoverageAnswer() ports.Completion {
	tok := func(text string, alts ...ports.Alternative) ports.Token {
		return ports.Token{Text: text, LogProb: math.Log(0.3), Alternatives: alts}
	}
	plain := func(text string) ports.Token { return ports.Token{Text: text, LogProb: math.Log(0.9)} }
	c := ports.Completion{Model: "a-model"}
	add := func(t ports.Token) { c.Text += t.Text; c.Tokens = append(c.Tokens, t) }

	add(plain(`{"`))
	add(plain(`focus`))
	add(plain(`":`))
	add(plain(` "`))
	add(tok(`backend`,
		ports.Alternative{Text: "backend", LogProb: math.Log(0.3)},
		ports.Alternative{Text: "frontend", LogProb: math.Log(0.25)},
		ports.Alternative{Text: "platform", LogProb: math.Log(0.2)},
		ports.Alternative{Text: "data", LogProb: math.Log(0.15)},
		ports.Alternative{Text: "other", LogProb: math.Log(0.1)}))
	add(plain(`"}`))
	return c
}

func TestCoverageReportsFullRepresentation(t *testing.T) {
	p := &recordingProvider{completion: fullCoverageAnswer()}
	got, err := app.NewJudge(p, testJudgeConfig()).Ask(context.Background(), "a short book", []ports.Question{noOptionInAlternativesQuestion()})
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	focus := got.Answers[0]
	if focus.Coverage.Represented != 5 {
		t.Errorf("represented = %d, want 5", focus.Coverage.Represented)
	}
	if focus.Coverage.Declared != 5 {
		t.Errorf("declared = %d, want 5", focus.Coverage.Declared)
	}
}
