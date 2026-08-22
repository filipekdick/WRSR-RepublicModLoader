#ifndef RML_LEGACY_CHAIN_POLICY_H
#define RML_LEGACY_CHAIN_POLICY_H

#include <stddef.h>
#include <stdint.h>

// Small platform-neutral helpers shared by the production Tesmio compatibility
// host and its regression tests.  These model only the legacy absolute-entry
// detour shape and the key Tesmio rule: a hook's expected bytes describe the
// CURRENT target state, not necessarily the original vanilla prologue.
static inline int rml_legacy_bytes_equal(const unsigned char* a, const unsigned char* b, size_t n) {
    if (!a || !b) return 0;
    for (size_t i = 0; i < n; ++i) if (a[i] != b[i]) return 0;
    return 1;
}

static inline int rml_legacy_expect_matches_current(const unsigned char* current,
                                                     const unsigned char* expect,
                                                     size_t stolen) {
    if (!expect) return 1; // Expected bytes are optional in the Tesmio hook contract.
    if (!current || !stolen) return 0;
    return rml_legacy_bytes_equal(current, expect, stolen);
}


// A shared Tesmio inline hook may legitimately request a shorter stolen span
// than the first owner.  The current target already contains RML's absolute
// entry detour across the first owner's canonical span, so publishing the new
// top over that same canonical span preserves the existing NOP padding while
// the later plugin only needs to validate the prefix it explicitly requested.
//
// Widening is different: the first owner's original trampoline returns to
// target + tracked_stolen.  Overwriting bytes beyond that boundary could make
// the preserved original path resume inside newly replaced bytes.  Do not
// permit that without rebuilding the existing owner's trampoline contract.
static inline int rml_legacy_shared_stolen_compatible(size_t tracked_stolen,
                                                       size_t requested_stolen) {
    return tracked_stolen >= 14u && requested_stolen >= 14u &&
           requested_stolen <= tracked_stolen;
}

static inline int rml_legacy_make_ff25_patch(unsigned char* out, size_t stolen, uintptr_t destination) {
    if (!out || stolen < 14u) return 0;
    for (size_t i = 0; i < stolen; ++i) out[i] = 0x90;
    out[0] = 0xFF; out[1] = 0x25;
    out[2] = out[3] = out[4] = out[5] = 0;
    for (int k = 0; k < 8; ++k) out[6 + k] = (unsigned char)(destination >> (k * 8));
    return 1;
}

static inline int rml_legacy_decode_ff25_patch(const unsigned char* bytes, size_t available, uintptr_t* destination) {
    if (!bytes || available < 14u) return 0;
    if (bytes[0] != 0xFF || bytes[1] != 0x25 || bytes[2] || bytes[3] || bytes[4] || bytes[5]) return 0;
    uintptr_t value = 0;
    for (int k = 0; k < 8; ++k) value |= ((uintptr_t)bytes[6 + k]) << (k * 8);
    if (destination) *destination = value;
    return value != 0;
}

#endif
