#ifndef RML_API_H
#define RML_API_H

#include <stdint.h>
#include <stddef.h>

#if defined(_WIN32)
  #define RML_CALL __cdecl
  #if defined(RML_PLUGIN_BUILD)
    #define RML_EXPORT extern "C" __declspec(dllexport)
  #else
    #define RML_EXPORT extern "C"
  #endif
#else
  #define RML_CALL
  #define RML_EXPORT extern "C"
#endif

#define RML_API_VERSION 1u
#define RML_PLUGIN_DESCRIPTOR_VERSION 1u

typedef enum RmlLogLevel {
    RML_LOG_TRACE = 0,
    RML_LOG_INFO  = 1,
    RML_LOG_WARN  = 2,
    RML_LOG_ERROR = 3
} RmlLogLevel;

typedef enum RmlSymbolConfidence {
    RML_SYMBOL_UNRESOLVED   = 0,
    RML_SYMBOL_PROBABLE     = 1,
    RML_SYMBOL_CONFIRMED    = 2,
    RML_SYMBOL_AMBIGUOUS    = 3,
    RML_SYMBOL_INCOMPATIBLE = 4,
    RML_SYMBOL_OBSERVED     = 5
} RmlSymbolConfidence;

typedef struct RmlPluginDescriptor {
    uint32_t struct_size;
    uint32_t descriptor_version;
    uint32_t api_min;
    uint32_t api_max;
    const char* id;
    const char* name;
    const char* version;
    const char* author;
} RmlPluginDescriptor;

typedef struct RmlSymbolInfo {
    uint32_t struct_size;
    const char* id;
    uintptr_t address;
    RmlSymbolConfidence confidence;
    const char* resolver;
} RmlSymbolInfo;

typedef struct RmlGameInfo {
    uint32_t struct_size;
    const char* version;
    const char* sha256;
    uintptr_t image_base;
    uint32_t loader_api;
    const char* loader_version;
} RmlGameInfo;

typedef struct RmlServiceInfo {
    uint32_t struct_size;
    const char* name;
    uint32_t version;
    const char* provider_id;
} RmlServiceInfo;

typedef struct RmlHostApi {
    uint32_t struct_size;
    uint32_t api_version;
    void* plugin_context;

    void (RML_CALL *log)(
        void* plugin_context,
        RmlLogLevel level,
        const char* message);

    int (RML_CALL *get_game_info)(
        void* plugin_context,
        RmlGameInfo* out_info);

    int (RML_CALL *get_symbol)(
        void* plugin_context,
        const char* symbol_id,
        RmlSymbolInfo* out_info);

    uint32_t (RML_CALL *get_symbol_count)(void* plugin_context);

    int (RML_CALL *get_symbol_by_index)(
        void* plugin_context,
        uint32_t index,
        RmlSymbolInfo* out_info);

    int (RML_CALL *provide_service)(
        void* plugin_context,
        const char* name,
        uint32_t version,
        void* service);

    void* (RML_CALL *consume_service)(
        void* plugin_context,
        const char* name,
        uint32_t version,
        RmlServiceInfo* out_info);
} RmlHostApi;

#define RML_HOST_API_SIZE ((uint32_t)sizeof(RmlHostApi))

typedef int (RML_CALL *RmlPluginQueryFn)(
    uint32_t host_api_version,
    RmlPluginDescriptor* out_descriptor);

typedef int (RML_CALL *RmlPluginLoadFn)(const RmlHostApi* host);
RML_EXPORT int RML_CALL RmlPluginQuery(
    uint32_t host_api_version,
    RmlPluginDescriptor* out_descriptor);
RML_EXPORT int RML_CALL RmlPluginLoad(const RmlHostApi* host);

#endif
