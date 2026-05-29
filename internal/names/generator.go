package names

import (
	"fmt"
	"math/rand"
	"time"
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

// Generate returns a random animal name (e.g. "otter") not present in existing.
func Generate(existing []string) string {
	return pick(existing, func(animal string) string { return animal })
}

// GenerateBranch returns an auto branch name like "fix/0527-1430-otter":
// fix/<mmdd>-<hhmm>-<animal>. The timestamp is Pacific wall-clock and leads the
// name so branches sort chronologically in name-ordered lists (tmux switcher,
// grove list); the animal trails as a memorable handle. Minute precision means
// existing only has to guard the rare same-minute collision.
func GenerateBranch(existing []string) string {
	stamp := time.Now().In(pacific()).Format("0102-1504")
	return pick(existing, func(animal string) string {
		return fmt.Sprintf("fix/%s-%s", stamp, animal)
	})
}

// pacific returns America/Los_Angeles (PST/PDT, DST-aware), falling back to the
// machine's local zone if the embedded zone database can't be loaded.
func pacific() *time.Location {
	if loc, err := time.LoadLocation("America/Los_Angeles"); err == nil {
		return loc
	}
	return time.Local
}

// pick returns the first name (built by format from an animal) not already in
// existing, falling back to numeric suffixes once every animal is taken.
func pick(existing []string, format func(animal string) string) string {
	used := make(map[string]bool, len(existing))
	for _, name := range existing {
		used[name] = true
	}

	perm := rand.Perm(len(animals))
	for _, i := range perm {
		if candidate := format(animals[i]); !used[candidate] {
			return candidate
		}
	}

	for n := 2; ; n++ {
		for _, a := range animals {
			if candidate := format(fmt.Sprintf("%s%d", a, n)); !used[candidate] {
				return candidate
			}
		}
	}
}
