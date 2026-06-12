package com.example.users;

import java.util.Optional;

public interface UserRepository {
    Optional<User> findById(String id);
    User save(User u);
}
