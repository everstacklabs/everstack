module everstack-sdk-tests

go 1.22.0

require github.com/everstacklabs/everstack-go v0.1.21

// The everstack-go module repo is not publicly resolvable, so build against the
// in-repo SDK source. Swap this for a real version once the module is published.
replace github.com/everstacklabs/everstack-go => ../../../packages/sdks/everstack-go
