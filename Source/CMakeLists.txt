cmake_minimum_required(VERSION 3.24)
file(STRINGS "${CMAKE_CURRENT_SOURCE_DIR}/VERSION" RML_VERSION LIMIT_COUNT 1)
project(RepublicModLoader VERSION ${RML_VERSION} LANGUAGES CXX)

if(NOT WIN32 OR NOT CMAKE_SIZEOF_VOID_P EQUAL 8)
    message(FATAL_ERROR "Republic Mod Loader native components require a 64-bit Windows toolchain.")
endif()

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

set(RML_NATIVE_OUTPUT "${CMAKE_BINARY_DIR}/native")

function(rml_native_target target)
    target_include_directories(${target} PRIVATE "${CMAKE_CURRENT_SOURCE_DIR}/include" "${CMAKE_CURRENT_SOURCE_DIR}/runtime")
    target_compile_definitions(${target} PRIVATE RML_BUILD_VERSION=\"${PROJECT_VERSION}\")
    set_target_properties(${target} PROPERTIES
        PREFIX ""
        RUNTIME_OUTPUT_DIRECTORY "${RML_NATIVE_OUTPUT}"
        LIBRARY_OUTPUT_DIRECTORY "${RML_NATIVE_OUTPUT}"
        ARCHIVE_OUTPUT_DIRECTORY "${RML_NATIVE_OUTPUT}"
    )
    if(MSVC OR CMAKE_CXX_SIMULATE_ID STREQUAL "MSVC")
        target_compile_options(${target} PRIVATE /O2 /GS- /GR- /Zl /W4)
        # RML supplies its own memcpy/memset/memcmp/strlen because the runtime
        # is linked /NODEFAULTLIB. Clang can otherwise fold those hand-written
        # loops back into calls to the same replacement symbols, producing
        # self-recursion. Disable builtin/libcall substitution for Clang builds.
        if(CMAKE_CXX_COMPILER_ID MATCHES "Clang")
            target_compile_options(${target} PRIVATE -fno-builtin)
        endif()
        target_link_options(${target} PRIVATE
            /NODEFAULTLIB /SUBSYSTEM:WINDOWS /DYNAMICBASE /HIGHENTROPYVA
            /NXCOMPAT /RELEASE /OPT:REF /OPT:ICF /MANIFEST:NO /ENTRY:DllMain)
    endif()
endfunction()

add_library(rml_runtime SHARED
    runtime/tiny.cpp
    runtime/log.cpp
    runtime/plan.cpp
    runtime/registry.cpp
    runtime/resolver.cpp
    runtime/tesmio_compat.cpp
    runtime/plugin_manager.cpp
    runtime/report.cpp
    runtime/menu_status.cpp
    runtime/journal.cpp
    runtime/workshop_filter.cpp
    runtime/deep_monitor.cpp
    runtime/runtime.cpp
)
rml_native_target(rml_runtime)
target_link_libraries(rml_runtime PRIVATE kernel32 ntdll)
