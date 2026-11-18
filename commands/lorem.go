package commands

import (
	"math/rand"
	"strconv"
	"strings"

	"github.com/jameymcelveen/zxcli/core"
)

func init() { core.Register("lorem", runLorem) }

var loremWords = strings.Fields(`lorem ipsum dolor sit amet consectetur adipiscing elit sed do eiusmod
tempor incididunt ut labore et dolore magna aliqua enim ad minim veniam quis nostrud exercitation
ullamco laboris nisi aliquip ex ea commodo consequat duis aute irure in reprehenderit voluptate
velit esse cillum fugiat nulla pariatur excepteur sint occaecat cupidatat non proident sunt culpa
qui officia deserunt mollit anim id est laborum`)

func runLorem(c *core.Context) error {
	fs := c.Flags()
	words := fs.Int("w", 0, "print exactly this many words instead of paragraphs")
	pos, err := c.Parse(fs)
	if err != nil {
		return err
	}
	paragraphs := 1
	if len(pos) > 0 {
		if paragraphs, err = strconv.Atoi(pos[0]); err != nil || paragraphs < 1 || paragraphs > 1000 {
			return core.Usagef("paragraphs must be a number from 1 to 1000, got %q", pos[0])
		}
	}
	if *words < 0 || *words > 100000 {
		return core.Usagef("word count must be between 1 and 100000")
	}
	rng := rand.New(rand.NewSource(rand.Int63()))
	if *words > 0 {
		c.Println(loremSentence(rng, *words, true))
		return nil
	}
	for p := 0; p < paragraphs; p++ {
		var sentences []string
		for s, count := 0, 4+rng.Intn(3); s < count; s++ {
			sentences = append(sentences, loremSentence(rng, 8+rng.Intn(9), p == 0 && s == 0))
		}
		if p > 0 {
			c.Println()
		}
		c.Println(strings.Join(sentences, " "))
	}
	return nil
}

// loremSentence builds one sentence of n words. The classic opening is used
// when classic is true.
func loremSentence(rng *rand.Rand, n int, classic bool) string {
	out := make([]string, n)
	for i := range out {
		if classic && i < len(loremWords) {
			out[i] = loremWords[i]
		} else {
			out[i] = loremWords[rng.Intn(len(loremWords))]
		}
	}
	s := strings.Join(out, " ")
	return strings.ToUpper(s[:1]) + s[1:] + "."
}
