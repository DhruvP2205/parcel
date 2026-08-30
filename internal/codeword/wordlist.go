package codeword

// wordlist is a fixed set of short, unambiguous-when-spoken English words
// used to build pairing codes. A 4-word code drawn without replacement from
// these 192 words has 192*191*190*189 ≈ 1,316,891,520 possible orderings —
// chosen over 3 words specifically to keep accidental code collisions
// negligible even with many codes live on one relay at once (see
// generate.go). See also internal/discovery's PBKDF2-stretched beacon tag,
// which is the layer that actually has to resist offline guessing.
var wordlist = []string{
	"anchor", "anvil", "arrow", "ash", "aspen", "atlas", "autumn", "badge",
	"badger", "banjo", "barley", "basil", "beacon", "beaver", "birch", "bishop",
	"blaze", "bloom", "bolt", "bramble", "brass", "brick", "bridge", "bronze",
	"brook", "cabin", "camel", "canary", "candle", "canyon", "cedar", "chalk",
	"charm", "cider", "cinder", "clover", "coast", "cobalt", "comet", "compass",
	"copper", "coral", "cotton", "crane", "crater", "crimson", "cricket", "crow",
	"crown", "cypress", "dandy", "dawn", "delta", "denim", "diesel", "dolphin",
	"dune", "eagle", "echo", "elder", "elm", "ember", "falcon", "fern",
	"fiddle", "finch", "fjord", "flame", "flint", "forge", "fox", "frost",
	"garnet", "gecko", "ginger", "glacier", "gorge", "granite", "grove", "gull",
	"harbor", "hazel", "heron", "hickory", "holly", "hornet", "husky", "indigo",
	"ivory", "ivy", "jasper", "jester", "juniper", "kestrel", "kiln", "lagoon",
	"lantern", "larch", "lark", "laurel", "lichen", "lily", "linen", "lotus",
	"lynx", "magpie", "maple", "marble", "marsh", "meadow", "mesa", "meteor",
	"mint", "moth", "nectar", "nettle", "newt", "nomad", "oak", "obsidian",
	"oleander", "olive", "onyx", "opal", "orchid", "osprey", "otter", "owl",
	"paddle", "pebble", "pelican", "pepper", "pheasant", "pine", "pixel", "plaza",
	"plum", "poplar", "prairie", "quail", "quartz", "quill", "raven", "reed",
	"ridge", "robin", "rocket", "rowan", "ruby", "sable", "saffron", "sage",
	"sail", "salt", "sequoia", "shale", "shrike", "sierra", "silver", "sparrow",
	"spruce", "steel", "stork", "sunset", "swallow", "sycamore", "tangerine", "teal",
	"tern", "thistle", "thorn", "thrush", "timber", "topaz", "trellis", "tulip",
	"tundra", "turtle", "twine", "umber", "vale", "velvet", "violet", "walnut",
	"warbler", "wattle", "willow", "wisteria", "wren", "yarrow", "yew", "zephyr",
}
