package policy

import (
	"reflect"
	"strings"
	"testing"
)

// TestParse_FindsEveryCommandALineRuns: quotes and escapes are removed, every
// simple command is found, the ones in subshells, substitutions and expanding
// heredocs included, and the expansions that make a plain word of a word are
// read as the shell reads them.
func TestParse_FindsEveryCommandALineRuns(t *testing.T) {
	cases := []struct {
		src  string
		want [][]string
	}{
		// Quoting and separators.
		{"ls -l", [][]string{{"ls", "-l"}}},
		{`echo "a b" 'c d' e\ f`, [][]string{{"echo", "a b", "c d", "e f"}}},
		{`echo "it's" 'say "hi"'`, [][]string{{"echo", "it's", `say "hi"`}}},
		{"a && b || c; d | e & f\ng", [][]string{{"a"}, {"b"}, {"c"}, {"d"}, {"e"}, {"f"}, {"g"}}},
		{"a |& b", [][]string{{"a"}, {"b"}}},
		{`echo '$(a)' \$HOME`, [][]string{{"echo", "$(a)", "$HOME"}}},
		{"echo a # git push", [][]string{{"echo", "a"}}},
		{"echo a#b", [][]string{{"echo", "a#b"}}},
		{"ec\\\nho hi", [][]string{{"echo", "hi"}}},
		{`echo "a > b" a\>b`, [][]string{{"echo", "a > b", "a>b"}}},
		{`echo a\`, [][]string{{"echo", `a\`}}},
		{`"git" push`, [][]string{{"git", "push"}}},
		{`g\it push`, [][]string{{"git", "push"}}},
		{`''`, [][]string{{""}}},
		{"", [][]string{}},
		{"  \n ; \n", [][]string{}},
		{"x=1 y=2 cmd arg", [][]string{{"x=", "y=", "cmd", "arg"}}},
		// Subshells, braces and substitutions.
		{"echo $(git push)", [][]string{{"echo", "$(git push)"}, {"git", "push"}}},
		{"echo `make check`", [][]string{{"echo", "`make check`"}, {"make", "check"}}},
		{`echo "x $(a) y"`, [][]string{{"echo", "x $(a) y"}, {"a"}}},
		{`echo "x ` + "`a`" + ` y"`, [][]string{{"echo", "x `a` y"}, {"a"}}},
		{"echo $(a $(b))", [][]string{{"echo", "$(a $(b))"}, {"a", "$(b)"}, {"b"}}},
		{"(a; b) && c", [][]string{{"a"}, {"b"}, {"c"}}},
		{"{ a; }", [][]string{{"{", "a"}, {"}"}}},
		{"echo ${x:-$(date)}", [][]string{{"echo", "${x:-$(date)}"}, {"date"}}},
		{"echo $((1+2)) $(( $(b) * 2 ))", [][]string{{"echo", "$((1+2))", "$(( $(b) * 2 ))"}, {"b"}}},
		{"diff <(a) <(b)", [][]string{{"diff", "<(a)", "<(b)"}, {"a"}, {"b"}}},
		{"echo `echo \\`git push\\``", [][]string{{"echo", "`echo \\`git push\\``"}, {"echo", "`git push`"}, {"git", "push"}}},
		{"echo `echo a\\\\b \\$HOME`", [][]string{{"echo", "`echo a\\\\b \\$HOME`"}, {"echo", "ab", "$HOME"}}},
		// Heredocs: the body is data, unless it expands.
		{"cat <<EOF\ngit push\nEOF\nls", [][]string{{"cat"}, {"ls"}}},
		{"cat <<'EOF'\n$(git push)\nEOF\nls", [][]string{{"cat"}, {"ls"}}},
		{"cat <<EOF\n$(git push)\nEOF\nls", [][]string{{"cat"}, {"git", "push"}, {"ls"}}},
		{"cat <<-EOF\n\tgit push\n\tEOF\nls", [][]string{{"cat"}, {"ls"}}},
		{"cat <<EOF | wc\nbody `a`\nEOF\nls", [][]string{{"cat"}, {"wc"}, {"a"}, {"ls"}}},
		{"x=$(cat <<EOF\ninside\nEOF\n) git status", [][]string{{"x=", "git", "status"}, {"cat"}}},
		{`cat <<"E O F"` + "\n$(git push)\nE O F\nls", [][]string{{"cat"}, {"ls"}}},
		// Arithmetic is no heredoc, and (( may be two subshells.
		{"(( 1 << 2 ))\ngit push", [][]string{{"git", "push"}}},
		{"echo $((1<<2))\ngit push", [][]string{{"echo", "$((1<<2))"}, {"git", "push"}}},
		{"(( x = $(git push) ))", [][]string{{"git", "push"}}},
		{"((cd x) && ls)", [][]string{{"cd", "x"}, {"ls"}}},
		{"((git push); (git status))", [][]string{{"git", "push"}, {"git", "status"}}},
		{"(( (git push) ); git status)", [][]string{{"git", "push"}, {"git", "status"}}},
		{"echo $((git push) )", [][]string{{"echo", "$((git push) )"}, {"git", "push"}}},
		{"echo $(( (1+2)*3 )) $((echo hi))", [][]string{{"echo", "$(( (1+2)*3 ))", "$((echo hi))"}}},
		{"echo $[ $(git push) + 1 ]", [][]string{{"echo", "$[", "$(git push)", "+", "1", "]"}, {"git", "push"}}},
		// ANSI-C and locale quoting.
		{`$'git' push`, [][]string{{"git", "push"}}},
		{`$'\x67it' push`, [][]string{{"git", "push"}}},
		{`$'\147it' push`, [][]string{{"git", "push"}}},
		{`$"git" push`, [][]string{{"git", "push"}}},
		{`echo $'it\'s' $'a\tb' $'x\\y'`, [][]string{{"echo", "it's", "a\tb", `x\y`}}},
		{`echo $'a\0b'`, [][]string{{"echo", "a"}}},
		{`echo $'\''; git push; echo x # '`, [][]string{{"echo", "'"}, {"git", "push"}, {"echo", "x"}}},
		{`echo "$'a'"`, [][]string{{"echo", "a"}}}, // quoted, and read all the same
		{`echo $'\a\b\e\E\f\n\r\v\"\?'`, [][]string{{"echo", "\a\b\x1b\x1b\f\n\r\v\"?"}}},
		{`echo $'g\U00000069\x74\xz\q\cA'`, [][]string{{"echo", `git\xz\q` + "\x01"}}},
		// Brace expansion, quoted or not.
		{"{git,push}", [][]string{{"git", "push"}}},
		{"echo {a,b}{1,2}", [][]string{{"echo", "a1", "a2", "b1", "b2"}}},
		{"echo x{,y} {a,{b,c}}", [][]string{{"echo", "x", "xy", "a", "b", "c"}}},
		{"echo {1..3} {c..a} {1..10..4} {01..03}", [][]string{{"echo", "1", "2", "3", "c", "b", "a", "1", "5", "9", "01", "02", "03"}}},
		{"echo {3..1..-2} {-02..1} {1..2..0} {a..c..x} {1..2..3..4} {ab..c}", [][]string{{"echo", "3", "1", "-02", "-01", "000", "001", "1", "2", "{a..c..x}", "{1..2..3..4}", "{ab..c}"}}},
		{`echo {a} {} ${x,y} a{b {a,b`, [][]string{{"echo", "{a}", "{}", "${x,y}", "a{b", "{a,b"}}},
		{`echo "{a,b}" '{a,b}' \{a,b\}`, [][]string{{"echo", "a", "b", "a", "b", "a", "b"}}},
		{"echo a{b{c,d}", [][]string{{"echo", "a{bc", "a{bd"}}},
		{"echo $(echo {a,b}) {x,y}", [][]string{{"echo", "$(echo {a,b})", "x", "y"}, {"echo", "a", "b"}}},
		// A variable that splits at once.
		{"git${IFS}push origin", [][]string{{"git", "push", "origin"}}},
		{"git$IFS'push'", [][]string{{"git", "push"}}},
		{"git$IFSx push", [][]string{{"git", "x", "push"}}},
		{`"git${IFS}push" x`, [][]string{{"git", "push", "x"}}},
		{"echo $IFS a", [][]string{{"echo", "a"}}},
	}
	for _, tc := range cases {
		if got := words(t, tc.src); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("parse(%q) = %q, want %q", tc.src, got, tc.want)
		}
	}
}

func TestParse_KeepsTheRedirectionsWithTheirDescriptors(t *testing.T) {
	cmds, err := parse("echo hi > out.txt 2>&1 < in 3>>log &> all 2> err >| forced {a,b}>x")
	if err != nil {
		t.Fatal(err)
	}
	want := []redirect{{"", ">", "out.txt"}, {"2", ">&", "1"}, {"", "<", "in"}, {"3", ">>", "log"}, {"", "&>", "all"}, {"2", ">", "err"}, {"", ">|", "forced"}, {"", ">", "x"}}
	if len(cmds) != 1 || !reflect.DeepEqual(cmds[0].words, []string{"echo", "hi", "a", "b"}) || !reflect.DeepEqual(cmds[0].redirects, want) {
		t.Errorf("parse = %+v, want echo hi a b with %+v", cmds, want)
	}

	// An ambiguous redirect is every target it could be.
	cmds, err = parse("echo a>{b,c}")
	if err != nil || len(cmds) != 1 || !reflect.DeepEqual(cmds[0].words, []string{"echo", "a"}) || len(cmds[0].redirects) != 2 || cmds[0].redirects[1].target != "c" {
		t.Errorf("parse(echo a>{b,c}) = %+v, %v", cmds, err)
	}
}

func TestParse_KeepsEachCommandAsWrittenForTheDenial(t *testing.T) {
	cmds, err := parse("  echo ok  &&   FOO=1 env make check  ; echo $(go test ./...)")
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, c := range cmds {
		texts = append(texts, c.text)
	}
	if want := []string{"echo ok", "FOO=1 env make check", "echo $(go test ./...)", "go test ./..."}; !reflect.DeepEqual(texts, want) {
		t.Errorf("texts = %q, want %q", texts, want)
	}
}

// TestParse_RefusesWhatItCannotBound: an expansion that makes more words than
// can be judged is not read, and neither is a line the hands' reader cannot
// read with certainty.
func TestParse_RefusesWhatItCannotBound(t *testing.T) {
	for _, src := range []string{
		"echo {1..100000}", "echo " + strings.Repeat("{a,b}", 20), "echo {a,b}{1..4000}", "echo {" + strings.Repeat("x", 600_000) + ",y}{a,b}",
		`echo $'abc`, `echo $'abc\`, "echo $[1<<2]\ngit push", "for ((i=0;i<1<<3;i++)); do git push; done", "cat > $'x",
	} {
		if cmds, err := parse(src); err == nil {
			t.Errorf("parse(%.40q) = %v, want an error", src, cmds)
		}
	}
}
