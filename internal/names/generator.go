package names

import (
	"fmt"
	"math/rand"
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

func Generate(existing []string) string {
	used := make(map[string]bool, len(existing))
	for _, name := range existing {
		used[name] = true
	}

	perm := rand.Perm(len(animals))
	for _, i := range perm {
		if !used[animals[i]] {
			return animals[i]
		}
	}

	for n := 2; ; n++ {
		for _, a := range animals {
			candidate := fmt.Sprintf("%s%d", a, n)
			if !used[candidate] {
				return candidate
			}
		}
	}
}
