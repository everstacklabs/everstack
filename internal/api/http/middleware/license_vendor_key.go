package middleware

// vendorLicensePublicKey is the Ed25519 key Everstack's license service signs
// license JWTs with (base64, as licpkg.NewVerifier expects). Gateways trust
// only this key, or EVS_LICENSE_PUBLIC_KEY when an operator overrides it (a dev
// cluster pointed at a dev license service). A key read from the gateway's own
// database is never trusted: anyone with write access to that database could
// otherwise store their own key and a license JWT they signed with it.
//
// To check it: https://license.everstack.ai/.well-known/jwks.json serves the
// same key (base64url "x"), with kid PsY8uqqhVh8, the first 8 bytes of its
// SHA-256 in raw base64. Key SHA-256 prefix 3ec63cbaaaa1561f.
//
// Rotating the license signing secret changes this key, so a rotation needs a
// gateway release carrying the new key before the license service switches.
const vendorLicensePublicKey = "FItIWIRBoGEvYrxX/tVcQTuqrnp3jUdvx3MHEoJNUMw="
