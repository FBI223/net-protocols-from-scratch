add_library(sctp STATIC
    src/packet.cpp
    src/chunk.cpp
    src/checksum.cpp
    src/association.cpp
    src/stream.cpp
)
target_include_directories(sctp PUBLIC include)
target_compile_options(sctp PRIVATE -Wall -Wextra)

add_executable(sctp_echo app/sctp_echo.cpp)
target_link_libraries(sctp_echo PRIVATE sctp)

add_executable(sctp_test tests/sctp_test.cpp)
target_link_libraries(sctp_test PRIVATE sctp)
add_test(NAME sctp COMMAND sctp_test)
