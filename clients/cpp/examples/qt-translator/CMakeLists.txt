add_executable(sigorest-qt-translator main.cpp)
target_link_libraries(sigorest-qt-translator PRIVATE sigorest-qt)
set_target_properties(sigorest-qt-translator PROPERTIES AUTOMOC ON)
