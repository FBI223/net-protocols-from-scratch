add_library(pfcp STATIC
    src/header.cpp
    src/ie.cpp
    src/message.cpp
    src/session.cpp
)
target_include_directories(pfcp PUBLIC include)
target_compile_options(pfcp PRIVATE -Wall -Wextra)

add_executable(pfcp_server app/pfcp_server.cpp)
target_link_libraries(pfcp_server PRIVATE pfcp)

add_executable(pfcp_test tests/pfcp_test.cpp)
target_link_libraries(pfcp_test PRIVATE pfcp)
add_test(NAME pfcp COMMAND pfcp_test)
