add_library(sigorest-core STATIC)

target_sources(sigorest-core
    PUBLIC
        include/sigorest/models.hpp
        include/sigorest/client.hpp
    PRIVATE
        src/client.cpp
)

target_include_directories(sigorest-core
    PUBLIC
        ${CMAKE_CURRENT_SOURCE_DIR}/include
        ${json_SOURCE_DIR}/include
)

target_link_libraries(sigorest-core
    PUBLIC
        CURL::libcurl
)
