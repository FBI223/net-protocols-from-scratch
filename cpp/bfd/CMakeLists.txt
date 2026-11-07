add_library(bfd STATIC
    src/packet.cpp
    src/session.cpp
    src/timers.cpp
)
target_include_directories(bfd PUBLIC include)
target_compile_options(bfd PRIVATE -Wall -Wextra)

add_executable(bfd_daemon app/bfd_daemon.cpp)
target_link_libraries(bfd_daemon PRIVATE bfd)

add_executable(bfd_test tests/bfd_test.cpp)
target_link_libraries(bfd_test PRIVATE bfd)
add_test(NAME bfd COMMAND bfd_test)
