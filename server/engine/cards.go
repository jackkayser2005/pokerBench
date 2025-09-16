package engine

import (
	"fmt"
	"math/rand"
	"time"
)

func NewDeck(seed int64) []Card {
	if seed == 0 {
		seed = time.Now().UnixNano()
	}
	r := rand.New(rand.NewSource(seed))
	var deck []Card
	for s := 0; s < 4; s++ {
		for rnk := 2; rnk <= 14; rnk++ {
			deck = append(deck, Card{Rank: rnk, Suit: "cdhs"[s]})
		}
	}
	for i := len(deck) - 1; i > 0; i-- {
		j := r.Intn(i + 1)
		deck[i], deck[j] = deck[j], deck[i]
	}
	return deck
}

func (c Card) String() string {
	ranks := "  23456789TJQKA"
	return fmt.Sprintf("%c%c", ranks[c.Rank], c.Suit)
}
