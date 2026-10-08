package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestCheckExpect(t *testing.T) {
	got := checkExpect("Hello World", []string{"hello", "foo|world", "bar"})
	if want := []string{"bar"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("missing = %v, want %v", got, want)
	}
}

func TestCheckReject(t *testing.T) {
	answer := "console.log 와 var 를 지웠습니다.\n\n```js\nconst x = 1;\ndocument.querySelector('#a').textContent = x;\n```\n"
	if got := checkReject(answer, []string{"console.", "var ", "$(|jQuery"}); len(got) != 0 {
		t.Fatalf("설명문은 검사하지 않아야 한다: found = %v", got)
	}

	answer = "고쳤습니다.\n```js\nvar x = $('#a').val();\nconsole.log(x);\n```"
	got := checkReject(answer, []string{"console.", "var ", "innerHTML", "$(|jQuery"})
	if want := []string{"console.", "var ", "$(|jQuery"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("found = %v, want %v", got, want)
	}

	// 펜스가 없으면 답변 전체를 본다.
	if got := checkReject("JQ('#a')", []string{"jq("}); len(got) != 1 {
		t.Fatalf("펜스 없는 답변도 검사해야 한다: found = %v", got)
	}
}

func TestCodeTextUnclosedFence(t *testing.T) {
	if got := strings.TrimSpace(codeText("앞\n```go\nfunc a() {}\n")); got != "func a() {}" {
		t.Fatalf("codeText = %q", got)
	}
}
