add_library(lpm-fib STATIC
    src/prefix.cpp
    src/trie.cpp
    src/dir24_8.cpp
    src/fib.cpp
)
target_include_directories(lpm-fib PUBLIC include)
target_compile_options(lpm-fib PRIVATE -Wall -Wextra)

add_executable(fib_bench app/fib_bench.cpp)
target_link_libraries(fib_bench PRIVATE lpm-fib)

add_executable(lpm_fib_test tests/lpm_fib_test.cpp)
target_link_libraries(lpm_fib_test PRIVATE lpm-fib)
add_test(NAME lpm-fib COMMAND lpm_fib_test)
