// Package quotes is the app's drill-sergeant voice: short lines in the style of
// David Goggins, shown when something is blocked, when focus time starts or ends,
// and as the line of the day. They are written for this app, not copied from his
// books or talks, and they are kept clean because a parent reads this dashboard too.
package quotes

import (
	"hash/fnv"
	"math/rand/v2"
)

type Kind int

const (
	Block Kind = iota // you reached for something that is blocked
	Start             // focus time is beginning
	Done              // focus time is over
	Daily             // the line of the day
)

var bank = map[Kind][]string{
	Block: {
		"Nobody is coming to save you. Close it and get back to work.",
		"Comfort is the enemy. You know that. Stay hard.",
		"You reached for the easy thing. Now go do the hard thing.",
		"Who's gonna carry the boats? Not the guy who just clicked that.",
		"That itch is your mind trying to quit. Don't let it win.",
		"Your mind will quit a hundred times before your body does. Not today.",
		"You're not tired. You're bored, and bored doesn't get a vote.",
		"Every time you say no to this, you get a little harder. Stack it.",
		"Nobody cares what you did yesterday. Win this hour.",
		"Discipline is doing it when you don't feel like it. You don't feel like it. Do it.",
		"Callous your mind. Say no, then say it again.",
		"Whining is easy. Working is rare. Which one are you?",
		"You were about to trade your future for a few minutes. Not on my watch.",
		"The easy way is crowded. Take the empty road.",
		"That was a weak move. Make the next one strong.",
		"Soft today, sorry tomorrow. Get back to it.",
	},
	Start: {
		"Time to get after it. Phone down, head down. Stay hard.",
		"The next few hours are yours to take. Go take them.",
		"No excuses, no negotiating. Start now.",
		"This is where it's earned. Callous your mind and begin.",
		"Motivation is a mood. Discipline is a decision. Decide.",
		"Show up when it's boring. That's where the work gets done.",
		"Who's gonna carry the boats? You are. Get moving.",
		"Hard work is the only shortcut that works. Get to it.",
	},
	Done: {
		"That's a rep. Bank it and go again tomorrow.",
		"You did what you said you'd do. That's rare. Respect it.",
		"Earned, not given. Rest, then stay hard.",
		"Every window you finish makes the next one easier. Stack the days.",
		"Proud of that? Good. Don't get comfortable.",
		"Another one in the bank. Most people quit before this point.",
	},
	Daily: {
		"Today is a chance to be harder than you were yesterday.",
		"You have more in the tank than you think. You're only at 40%.",
		"Don't wait for motivation. Build the habit that doesn't need it.",
		"The only person you're racing is who you were yesterday.",
		"Suffer a little now so you can be proud later.",
		"Be uncommon. Do the work everyone else skips.",
		"Small wins stack. Stack them every single day.",
		"Callous your mind: do one thing today you don't want to do.",
		"Nobody's coming to do it for you. That's the good news. It's all yours.",
		"Hit the gym, earn the night. Skip it, and the night is a lot less fun.",
		"Stay hard. It's not a mood, it's a choice you make at 5 PM.",
		"A tired body is not a finished body. Go again.",
	},
}

// Random is a line for the moment, chosen fresh each time.
func Random(k Kind) string {
	b := bank[k]
	return b[rand.IntN(len(b))]
}

// ForDay is the line of the day: the same all day, different tomorrow.
func ForDay(day string) string {
	h := fnv.New32a()
	h.Write([]byte(day))
	b := bank[Daily]
	return b[h.Sum32()%uint32(len(b))]
}
