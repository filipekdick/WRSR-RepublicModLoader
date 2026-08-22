#ifndef RML_WIN32_MIN_H
#define RML_WIN32_MIN_H

#include <stdint.h>
#include <stddef.h>

#define WIN32_LEAN_AND_MEAN
#define WINAPI __stdcall
#define CALLBACK __stdcall
#define TRUE 1
#define FALSE 0

typedef int BOOL;
typedef uint8_t BYTE;
typedef uint16_t WORD;
typedef uint32_t DWORD;
typedef int32_t LONG;
typedef uint64_t DWORD64;
typedef uint64_t ULONG_PTR;
typedef size_t SIZE_T;
typedef void* HANDLE;
typedef void* HMODULE;
typedef void* HINSTANCE;
typedef void* FARPROC;
typedef void* LPVOID;
typedef const void* LPCVOID;
typedef wchar_t WCHAR;
typedef const WCHAR* LPCWSTR;
typedef WCHAR* LPWSTR;
typedef const char* LPCSTR;
typedef char* LPSTR;
typedef unsigned int UINT;
typedef uint64_t ULONGLONG;
typedef int64_t LONGLONG;
typedef LONG HRESULT;
typedef DWORD (WINAPI *LPTHREAD_START_ROUTINE)(LPVOID);

typedef struct _SYSTEMTIME {
    WORD wYear, wMonth, wDayOfWeek, wDay;
    WORD wHour, wMinute, wSecond, wMilliseconds;
} SYSTEMTIME;

typedef union _LARGE_INTEGER {
    struct { DWORD LowPart; LONG HighPart; };
    int64_t QuadPart;
} LARGE_INTEGER;

typedef struct _MEMORY_BASIC_INFORMATION {
    LPVOID BaseAddress;
    LPVOID AllocationBase;
    DWORD AllocationProtect;
    WORD PartitionId;
    SIZE_T RegionSize;
    DWORD State;
    DWORD Protect;
    DWORD Type;
} MEMORY_BASIC_INFORMATION;


#define DLL_PROCESS_DETACH 0
#define DLL_PROCESS_ATTACH 1
#define INVALID_HANDLE_VALUE ((HANDLE)(intptr_t)-1)
#define INVALID_FILE_ATTRIBUTES 0xFFFFFFFFu
#define GENERIC_READ  0x80000000u
#define GENERIC_WRITE 0x40000000u
#define FILE_SHARE_READ  0x00000001u
#define FILE_SHARE_WRITE 0x00000002u
#define CREATE_ALWAYS 2u
#define OPEN_EXISTING 3u
#define OPEN_ALWAYS 4u
#define FILE_BEGIN 0u
#define FILE_CURRENT 1u
#define FILE_END 2u
#define FILE_ATTRIBUTE_NORMAL 0x00000080u
#define FILE_ATTRIBUTE_DIRECTORY 0x00000010u
#define MEM_COMMIT  0x00001000u
#define MEM_RESERVE 0x00002000u
#define MEM_RELEASE 0x00008000u
#define MEM_FREE    0x00010000u
#define PAGE_NOACCESS           0x01u
#define PAGE_READONLY           0x02u
#define PAGE_READWRITE          0x04u
#define PAGE_EXECUTE            0x10u
#define PAGE_EXECUTE_READ       0x20u
#define PAGE_EXECUTE_READWRITE  0x40u
#define PAGE_EXECUTE_WRITECOPY  0x80u
#define PAGE_GUARD              0x100u
#define CP_UTF8 65001u
#define TLS_OUT_OF_INDEXES 0xFFFFFFFFu
#define HEAP_ZERO_MEMORY 0x00000008u

extern "C" {
__declspec(dllimport) BOOL WINAPI DisableThreadLibraryCalls(HMODULE);
__declspec(dllimport) HMODULE WINAPI GetModuleHandleW(LPCWSTR);
__declspec(dllimport) DWORD WINAPI GetModuleFileNameW(HMODULE, LPWSTR, DWORD);
__declspec(dllimport) FARPROC WINAPI GetProcAddress(HMODULE, LPCSTR);
__declspec(dllimport) HMODULE WINAPI LoadLibraryW(LPCWSTR);
__declspec(dllimport) BOOL WINAPI FreeLibrary(HMODULE);
__declspec(dllimport) LPVOID WINAPI VirtualAlloc(LPVOID, SIZE_T, DWORD, DWORD);
__declspec(dllimport) BOOL WINAPI VirtualFree(LPVOID, SIZE_T, DWORD);
__declspec(dllimport) BOOL WINAPI VirtualProtect(LPVOID, SIZE_T, DWORD, DWORD*);
__declspec(dllimport) SIZE_T WINAPI VirtualQuery(LPCVOID, MEMORY_BASIC_INFORMATION*, SIZE_T);
__declspec(dllimport) BOOL WINAPI FlushInstructionCache(HANDLE, LPCVOID, SIZE_T);
__declspec(dllimport) HANDLE WINAPI GetCurrentProcess(void);
__declspec(dllimport) DWORD WINAPI GetCurrentProcessId(void);
__declspec(dllimport) HANDLE WINAPI CreateThread(LPVOID, SIZE_T, LPTHREAD_START_ROUTINE, LPVOID, DWORD, DWORD*);
__declspec(dllimport) void WINAPI Sleep(DWORD);
__declspec(dllimport) HANDLE WINAPI CreateFileW(LPCWSTR, DWORD, DWORD, LPVOID, DWORD, DWORD, HANDLE);
__declspec(dllimport) BOOL WINAPI ReadFile(HANDLE, LPVOID, DWORD, DWORD*, LPVOID);
__declspec(dllimport) BOOL WINAPI WriteFile(HANDLE, LPCVOID, DWORD, DWORD*, LPVOID);
__declspec(dllimport) BOOL WINAPI CloseHandle(HANDLE);
__declspec(dllimport) BOOL WINAPI GetFileSizeEx(HANDLE, LARGE_INTEGER*);
__declspec(dllimport) BOOL WINAPI SetFilePointerEx(HANDLE, LARGE_INTEGER, LARGE_INTEGER*, DWORD);
__declspec(dllimport) BOOL WINAPI CreateDirectoryW(LPCWSTR, LPVOID);
__declspec(dllimport) DWORD WINAPI GetFileAttributesW(LPCWSTR);
__declspec(dllimport) void WINAPI GetLocalTime(SYSTEMTIME*);
__declspec(dllimport) void WINAPI GetSystemTime(SYSTEMTIME*);
__declspec(dllimport) DWORD WINAPI GetCurrentThreadId(void);
__declspec(dllimport) ULONGLONG WINAPI GetTickCount64(void);
__declspec(dllimport) DWORD WINAPI TlsAlloc(void);
__declspec(dllimport) LPVOID WINAPI TlsGetValue(DWORD);
__declspec(dllimport) BOOL WINAPI TlsSetValue(DWORD, LPVOID);
__declspec(dllimport) int WINAPI WideCharToMultiByte(UINT, DWORD, LPCWSTR, int, LPSTR, int, LPCSTR, BOOL*);
__declspec(dllimport) int WINAPI MultiByteToWideChar(UINT, DWORD, LPCSTR, int, LPWSTR, int);
__declspec(dllimport) DWORD WINAPI GetLastError(void);
__declspec(dllimport) HANDLE WINAPI GetProcessHeap(void);
__declspec(dllimport) LPVOID WINAPI HeapAlloc(HANDLE, DWORD, SIZE_T);
__declspec(dllimport) BOOL WINAPI HeapFree(HANDLE, DWORD, LPVOID);
}

// Keep the interlocked primitives in generated x64 machine code rather than
// importing individual Interlocked* names through the hand-authored import lib.
// This removes an entire class of loader-time import-resolution failures.
extern "C" long _InterlockedExchange(volatile long*, long);
extern "C" long _InterlockedCompareExchange(volatile long*, long, long);
extern "C" long _InterlockedIncrement(volatile long*);
extern "C" long long _InterlockedIncrement64(volatile long long*);
#pragma intrinsic(_InterlockedExchange)
#pragma intrinsic(_InterlockedCompareExchange)
#pragma intrinsic(_InterlockedIncrement)
#pragma intrinsic(_InterlockedIncrement64)
static __forceinline LONG RmlInterlockedExchange(volatile LONG* value, LONG exchange) {
    return (LONG)_InterlockedExchange((volatile long*)value, (long)exchange);
}
static __forceinline LONG RmlInterlockedCompareExchange(volatile LONG* value, LONG exchange, LONG comparand) {
    return (LONG)_InterlockedCompareExchange((volatile long*)value, (long)exchange, (long)comparand);
}
static __forceinline LONG RmlInterlockedIncrement(volatile LONG* value) {
    return (LONG)_InterlockedIncrement((volatile long*)value);
}
static __forceinline LONGLONG RmlInterlockedIncrement64(volatile LONGLONG* value) {
    return (LONGLONG)_InterlockedIncrement64((volatile long long*)value);
}
#endif
