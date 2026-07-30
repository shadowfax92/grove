package names

import (
	"fmt"
	"math/rand"
	"time"
	_ "time/tzdata"
)

var animals = []string{
	"albatross", "alpaca", "ant", "anteater", "antelope", "ape", "armadillo", "axolotl",
	"baboon", "badger", "barracuda", "bat", "bear", "beaver", "bee", "bison", "bluejay",
	"boar", "bobcat", "buffalo", "butterfly",
	"camel", "capybara", "cardinal", "caribou", "cassowary", "cat", "caterpillar",
	"chameleon", "cheetah", "chickadee", "chipmunk", "clam", "cobra", "condor",
	"coral", "cougar", "cow", "coyote", "crab", "crane", "cricket", "crocodile", "crow",
	"deer", "dingo", "dolphin", "donkey", "dove", "dragonfly", "duck", "dugong",
	"eagle", "eel", "egret", "elephant", "elk", "emu", "ermine",
	"falcon", "ferret", "finch", "firefly", "flamingo", "fox", "frog",
	"gazelle", "gecko", "giraffe", "goat", "goose", "gopher", "gorilla",
	"grasshopper", "grouse", "grizzly", "gull",
	"hamster", "hare", "hawk", "hedgehog", "heron", "herring", "hippo",
	"hornet", "horse", "hummingbird", "hyena",
	"ibis", "iguana", "impala",
	"jackal", "jaguar", "jay", "jellyfish",
	"kangaroo", "kingfisher", "kiwi", "koala", "koi", "kookaburra",
	"ladybug", "lark", "lemming", "lemur", "leopard", "lion", "llama",
	"lobster", "locust", "loon", "lynx",
	"macaw", "magpie", "mammoth", "manatee", "mantis", "marmot", "marten",
	"meadowlark", "meerkat", "mink", "mole", "mongoose", "monkey", "moose",
	"moth", "mouse", "mule", "muskrat",
	"narwhal", "newt", "nighthawk", "nutria",
	"ocelot", "octopus", "opossum", "orangutan", "orca", "oriole", "osprey",
	"ostrich", "otter", "owl", "ox", "oyster",
	"panda", "pangolin", "panther", "parrot", "partridge", "peacock", "pelican",
	"penguin", "pheasant", "pike", "pika", "piranha", "platypus", "plover",
	"pony", "porcupine", "possum", "puma", "python",
	"quail", "quokka",
	"rabbit", "raccoon", "ram", "raven", "reindeer", "robin", "rooster",
	"salamander", "salmon", "sandpiper", "sardine", "scorpion", "seahorse",
	"seal", "shark", "sheep", "shrew", "shrike", "skunk", "sloth", "snail",
	"snake", "sparrow", "spider", "squid", "squirrel", "starling", "stingray",
	"stork", "sturgeon", "swan",
	"tapir", "tarsier", "termite", "tern", "thrush", "tiger", "toad",
	"toucan", "trout", "tuna", "turkey", "turtle",
	"urchin",
	"viper", "vole", "vulture",
	"walrus", "warthog", "wasp", "weasel", "whale", "wolf", "wolverine",
	"wombat", "woodpecker", "wren",
	"yak",
	"zebra",
}

var warmAdjectives = []string{
	"affectionate", "bright", "calm", "cheerful", "cheery", "cozy", "cuddly", "dear",
	"dreamy", "easygoing", "friendly", "gentle", "glowing", "golden", "gracious", "happy",
	"hearty", "honeyed", "hopeful", "jolly", "kind", "kindly", "lovely", "mellow",
	"merry", "neighborly", "peaceful", "plucky", "rosy", "serene", "snug", "soft",
	"sunny", "sweet", "tender", "thoughtful", "tranquil", "twinkly", "warm", "welcoming",
	"winsome",
}

var cuteAnimals = []string{
	"alpaca", "axolotl", "bear", "beaver", "bunny", "butterfly", "capybara", "cat",
	"chickadee", "chipmunk", "duck", "duckling", "fawn", "ferret", "finch", "firefly",
	"fox", "frog", "gecko", "goat", "goose", "hamster", "hare", "hedgehog",
	"hummingbird", "kitten", "kiwi", "koala", "ladybug", "lamb", "lemur", "llama",
	"manatee", "marmot", "meerkat", "mouse", "narwhal", "newt", "otter", "owl",
	"panda", "pangolin", "penguin", "piglet", "pika", "platypus", "pony", "puffin",
	"puppy", "quail", "quokka", "rabbit", "raccoon", "redpanda", "robin", "seal",
	"shrew", "sloth", "snail", "sparrow", "squirrel", "stoat", "swan", "tapir",
	"turtle", "wallaby", "wombat", "wren",
}

// Generate returns a random animal name (e.g. "otter") not present in existing.
func Generate(existing []string) string {
	return pick(existing, animals, func(animal string) string { return animal })
}

// GenerateBranch returns an auto branch name like "feat/07-30-cozy-otter".
func GenerateBranch(existing []string) string {
	return generateBranchAt(existing, time.Now())
}

func generateBranchAt(existing []string, now time.Time) string {
	stamp := now.In(pacific()).Format("01-02")
	candidates := make([]string, 0, len(warmAdjectives)*len(cuteAnimals))
	for _, adjective := range warmAdjectives {
		for _, animal := range cuteAnimals {
			candidates = append(candidates, adjective+"-"+animal)
		}
	}
	return pick(existing, candidates, func(candidate string) string {
		return "feat/" + stamp + "-" + candidate
	})
}

func pacific() *time.Location {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		panic(fmt.Sprintf("loading Pacific timezone: %v", err))
	}
	return loc
}

// pick returns an unused formatted choice, adding numeric suffixes after exhaustion.
func pick(existing, choices []string, format func(string) string) string {
	used := make(map[string]bool, len(existing))
	for _, name := range existing {
		used[name] = true
	}

	perm := rand.Perm(len(choices))
	for _, i := range perm {
		if candidate := format(choices[i]); !used[candidate] {
			return candidate
		}
	}

	for n := 2; ; n++ {
		for _, choice := range choices {
			if candidate := format(fmt.Sprintf("%s%d", choice, n)); !used[candidate] {
				return candidate
			}
		}
	}
}
