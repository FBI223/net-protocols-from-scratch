add_library(nat-conntrack STATIC
    src/flow_key.cpp
    src/conntrack.cpp
    src/nat.cpp
    src/checksum.cpp
    src/port_pool.cpp
)
target_include_directories(nat-conntrack PUBLIC include)
target_compile_options(nat-conntrack PRIVATE -Wall -Wextra)

add_executable(nat_sim app/nat_sim.cpp)
target_link_libraries(nat_sim PRIVATE nat-conntrack)

add_executable(nat_conntrack_test tests/nat_conntrack_test.cpp)
target_link_libraries(nat_conntrack_test PRIVATE nat-conntrack)
add_test(NAME nat-conntrack COMMAND nat_conntrack_test)
