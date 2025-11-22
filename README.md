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
├── database/
│   └── seeder.go       <-- (El código del sembrador que te di antes)
├── docker-compose.yml  <-- (El archivo YAML para Mongo y Redis)
├── .gitignore
├── go.mod
└── go.sum

Ejecuta:

go mod tidy