Archivo gitigoner:

# Ignorar carpetas de configuración de IDE
.vscode/
.idea/

# Ignorar binarios y ejecutables
*.exe
*.dll
*.so

# Ignorar Datos Pesados (Dataset)
ml-10M100K/
ml-10m.zip
*.dat

# Ignorar carpetas de datos de Docker (si las mapeas localmente)
mongo_data/

Estructura inicial de carpetas:

CC65-TrabajoFinal/
├── api/
│   └── Dockerfile 
│   └── main.go 
├── worker/
│   └── Dockerfile 
│   └── main.go 
├── database/
│   └── seeder.go      
├── ml-10M100K/ <- Dataset unzippeado 
├── docker-compose.yml 
├── .gitignore
├── go.mod
└── go.sum
└── README.md

Ejecuta:

go mod tidy