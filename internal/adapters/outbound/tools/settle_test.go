package tools_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
)

func TestClaimsSettleTool(t *testing.T) {
	out, err := tools.NewClaimsSettle().Invoke(context.Background(), map[string]string{
		"fields": `{"picked":[{"citations":[{"id":0,"quote":"Logged every passing ship","status":"grounded"}]}]}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	res := out.(map[string]any)
	if res["gaps"] != 0 || !reflect.DeepEqual(res["kept"], map[string][]string{"picked": {"Logged every passing ship"}}) {
		t.Errorf("result %v", res)
	}
	if _, err := tools.NewClaimsSettle().Invoke(context.Background(), map[string]string{"fields": "{"}); err == nil {
		t.Error("bad json accepted")
	}
}
