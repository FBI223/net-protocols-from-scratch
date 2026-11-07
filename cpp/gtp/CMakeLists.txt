add_library(gtp STATIC
    src/header.cpp
    src/gtpu.cpp
    src/gtpc.cpp
    src/ie.cpp
    src/tunnel.cpp
)
target_include_directories(gtp PUBLIC include)
target_compile_options(gtp PRIVATE -Wall -Wextra)

add_executable(gtpu_relay app/gtpu_relay.cpp)
target_link_libraries(gtpu_relay PRIVATE gtp)

add_executable(gtp_test tests/gtp_test.cpp)
target_link_libraries(gtp_test PRIVATE gtp)
add_test(NAME gtp COMMAND gtp_test)
