#include "runtime_internal.h"

void* rml_alloc(size_t size, int zero) {
    if (!size) return 0;
    return HeapAlloc(GetProcessHeap(), zero ? HEAP_ZERO_MEMORY : 0, size);
}

void rml_free(void* ptr) {
    if (ptr) HeapFree(GetProcessHeap(), 0, ptr);
}

size_t rml_strlen(const char* s) {
    size_t n = 0;
    if (!s) return 0;
    while (s[n]) ++n;
    return n;
}

size_t rml_wcslen(const wchar_t* s) {
    size_t n = 0;
    if (!s) return 0;
    while (s[n]) ++n;
    return n;
}

static wchar_t lower_ascii_w(wchar_t c) {
    if (c >= L'A' && c <= L'Z') return (wchar_t)(c + (L'a' - L'A'));
    return c;
}

int rml_strcmp(const char* a, const char* b) {
    if (a == b) return 0;
    if (!a) return -1;
    if (!b) return 1;
    while (*a && *a == *b) { ++a; ++b; }
    return (unsigned char)*a - (unsigned char)*b;
}

int rml_wstrcmp_i(const wchar_t* a, const wchar_t* b) {
    if (a == b) return 0;
    if (!a) return -1;
    if (!b) return 1;
    while (*a && lower_ascii_w(*a) == lower_ascii_w(*b)) { ++a; ++b; }
    return (int)lower_ascii_w(*a) - (int)lower_ascii_w(*b);
}

void rml_memcpy(void* dst, const void* src, size_t n) {
    BYTE* d = (BYTE*)dst;
    const BYTE* s = (const BYTE*)src;
    for (size_t i = 0; i < n; ++i) d[i] = s[i];
}

void rml_memset(void* dst, int value, size_t n) {
    BYTE* d = (BYTE*)dst;
    for (size_t i = 0; i < n; ++i) d[i] = (BYTE)value;
}

int rml_memeq(const void* a, const void* b, size_t n) {
    const BYTE* x = (const BYTE*)a;
    const BYTE* y = (const BYTE*)b;
    for (size_t i = 0; i < n; ++i) if (x[i] != y[i]) return 0;
    return 1;
}

void rml_copy_str(char* dst, size_t cap, const char* src) {
    if (!dst || !cap) return;
    size_t i = 0;
    if (src) for (; i + 1 < cap && src[i]; ++i) dst[i] = src[i];
    dst[i] = 0;
}


void rml_append_hex(char* dst, size_t cap, uint32_t value) {
    if (!dst || !cap) return;
    static const char* h="0123456789ABCDEF";
    char tmp[9]; for (int i=7;i>=0;--i) { tmp[i]=h[value&15u]; value>>=4; } tmp[8]=0;
    size_t p=rml_strlen(dst), first=0; while (first<7 && tmp[first]=='0') ++first;
    for (; first<8 && p+1<cap; ++first) dst[p++]=tmp[first];
    dst[p]=0;
}

void rml_copy_wstr(wchar_t* dst, size_t cap, const wchar_t* src) {
    if (!dst || !cap) return;
    size_t i = 0;
    if (src) for (; i + 1 < cap && src[i]; ++i) dst[i] = src[i];
    dst[i] = 0;
}

int rml_join_path(wchar_t* out, size_t cap, const wchar_t* a, const wchar_t* b) {
    if (!out || !cap || !a || !b) return 0;
    size_t p = 0;
    while (a[p] && p + 1 < cap) { out[p] = a[p]; ++p; }
    if (p && out[p - 1] != L'\\' && out[p - 1] != L'/') {
        if (p + 1 >= cap) return 0;
        out[p++] = L'\\';
    }
    size_t j = 0;
    while (b[j] && p + 1 < cap) out[p++] = b[j++];
    out[p] = 0;
    return b[j] == 0;
}

int rml_utf16_to_utf8(const wchar_t* in, char* out, int cap) {
    if (!in || !out || cap <= 0) return 0;
    int n = WideCharToMultiByte(CP_UTF8, 0, in, -1, out, cap, 0, 0);
    if (!n) out[0] = 0;
    return n;
}

extern "C" void* memcpy(void* dst, const void* src, size_t n) {
    rml_memcpy(dst, src, n);
    return dst;
}

extern "C" void* memset(void* dst, int value, size_t n) {
    rml_memset(dst, value, n);
    return dst;
}

extern "C" int memcmp(const void* a, const void* b, size_t n) {
    const BYTE* x = (const BYTE*)a;
    const BYTE* y = (const BYTE*)b;
    for (size_t i = 0; i < n; ++i) {
        if (x[i] != y[i]) return (int)x[i] - (int)y[i];
    }
    return 0;
}

extern "C" size_t strlen(const char* s) { return rml_strlen(s); }
