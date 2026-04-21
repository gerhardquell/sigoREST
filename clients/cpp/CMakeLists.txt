cmake_minimum_required(VERSION 3.16)
project(sigorest-cpp VERSION 1.0.0 LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

# Options
option(SIGOREST_BUILD_QT "Build Qt wrapper" ON)
option(SIGOREST_BUILD_EXAMPLES "Build examples" ON)

# Dependencies
find_package(CURL REQUIRED)
include(FetchContent)
FetchContent_Declare(
    json
    URL https://github.com/nlohmann/json/releases/download/v3.11.3/include.zip
    DOWNLOAD_EXTRACT_TIMESTAMP TRUE
)
FetchContent_MakeAvailable(json)

# Core library
add_subdirectory(core)

# Qt wrapper
if(SIGOREST_BUILD_QT)
    find_package(Qt6 COMPONENTS Core Network)
    if(Qt6_FOUND)
        add_subdirectory(qt)
    else()
        message(WARNING "Qt6 not found, skipping Qt wrapper")
    endif()
endif()

# Examples
if(SIGOREST_BUILD_EXAMPLES)
    add_subdirectory(examples/basic)
    if(SIGOREST_BUILD_QT AND Qt6_FOUND)
        add_subdirectory(examples/qt-translator)
    endif()
endif()
