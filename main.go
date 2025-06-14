package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi"
	"github.com/go-chi/cors"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/swarajzz/rss-agg/internal/config"
	"github.com/swarajzz/rss-agg/internal/database"

	_ "github.com/lib/pq"
)

type apiConfig struct {
	DB     *database.Queries
	config *config.Config
}

type command struct {
	name      string
	arguments []string
}

type commands struct {
	validCommands map[string]func(*apiConfig, command) error
}

func handlerLogin(s *apiConfig, cmd command) error {
	if len(cmd.arguments) == 0 {
		err := fmt.Errorf("username is required")
		return err
	}

	name := cmd.arguments[0]

	if _, ok := s.DB.GetUserByName(context.Background(), name); ok != nil {
		log.Fatalf("user %v not found", name)
	}

	s.config.SetUser(name)
	fmt.Printf("User %v is logged in\n", name)
	return nil
}

func handlerRegister(s *apiConfig, cmd command) error {
	if len(cmd.arguments) == 0 {
		err := fmt.Errorf("name is required")
		return err
	}

	name := cmd.arguments[0]

	user, err := s.DB.CreateUser(context.Background(), database.CreateUserParams{
		ID:        uuid.New(),
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		Name:      name,
	})
	if err != nil {
		log.Fatal(err)
	}

	s.config.SetUser(user.Name)

	fmt.Println("Username" + name + "successfully registered")
	return nil
}

func handlerUsers(s *apiConfig, cmd command) error {
	users, err := s.DB.GetUsers(context.Background())
	if err != nil {
		log.Fatal(err)
	}

	for _, user := range users {
		fmt.Println(user.Name)
	}
	return nil
}

func (c *commands) run(s *apiConfig, cmd command) error {
	if handler, ok := c.validCommands[cmd.name]; ok {
		return handler(s, cmd)
	}
	return fmt.Errorf("invalid command: %s", cmd.name)
}

func (c *commands) register(name string, f func(*apiConfig, command) error) {
	c.validCommands[name] = f
}

func main() {
	godotenv.Load(".env")

	port := os.Getenv("PORT")
	if port == "" {
		log.Fatal("PORT is not found in the environment")
	}

	dbURL := os.Getenv("DB_URL")
	if dbURL == "" {
		log.Fatal("DB_URL is not found in the environment")
	}

	conn, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatal("Can't connect to database", err)
	}

	if len(os.Args) < 2 {
		fmt.Println("No command provided.")
		os.Exit(1)
	}

	cmdName := os.Args[1]
	cmdArgs := os.Args[2:]

	cfg, err := config.Read()
	if err != nil {
		log.Fatal(err)
	}

	db := database.New(conn)
	apiCfg := apiConfig{
		DB:     db,
		config: &cfg,
	}

	cmds := &commands{
		validCommands: make(map[string]func(*apiConfig, command) error),
	}

	cmds.register("login", handlerLogin)
	cmds.register("register", handlerRegister)
	cmds.register("users", handlerUsers)

	cmd := command{
		name:      cmdName,
		arguments: cmdArgs,
	}

	err = cmds.run(&apiCfg, cmd)
	if err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}

	go startScraping(db, 10, time.Minute)

	router := chi.NewRouter()

	router.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"https://*", "http://*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"*"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: false,
		MaxAge:           300,
	}))

	v1Router := chi.NewRouter()
	v1Router.Get("/healthz", handlerReadiness)
	v1Router.Get("/err", handlerErr)
	v1Router.Post("/users", apiCfg.handlerCreateUser)
	v1Router.Get("/users", apiCfg.middlewareAuth(apiCfg.handlerGetUser))

	v1Router.Post("/feeds", apiCfg.middlewareAuth(apiCfg.handlerCreateFeed))
	v1Router.Get("/feeds", apiCfg.handlerGetFeeds)

	v1Router.Post("/feed_follows", apiCfg.middlewareAuth(apiCfg.handlerCreateFeedFollow))
	v1Router.Get("/feed_follows", apiCfg.middlewareAuth(apiCfg.handlerGetFeedFollows))
	v1Router.Delete("/feed_follows/{feedFollowID}", apiCfg.middlewareAuth(apiCfg.handlerDeleteFeedFollow))

	v1Router.Get("/posts", apiCfg.middlewareAuth(apiCfg.handlerGetPostsForUser))

	router.Mount("/v1", v1Router)
	srv := &http.Server{
		Addr:    ":" + port,
		Handler: router,
	}

	log.Printf("Serving on port: %s\n", port)
	log.Fatal(srv.ListenAndServe())
}
