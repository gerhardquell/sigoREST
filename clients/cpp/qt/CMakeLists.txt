add_library(sigorest-qt STATIC)

target_sources(sigorest-qt
    PUBLIC
        include/sigorest/qt/client.hpp
    PRIVATE
        src/client.cpp
)

target_include_directories(sigorest-qt
    PUBLIC
        ${CMAKE_CURRENT_SOURCE_DIR}/include
)

target_link_libraries(sigorest-qt
    PUBLIC
        sigorest-core
        Qt6::Core
        Qt6::Network
)
