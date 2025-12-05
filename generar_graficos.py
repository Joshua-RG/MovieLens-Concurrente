import pandas as pd
import matplotlib.pyplot as plt
import seaborn as sns
import io
import numpy as np

# --- 1. CARGA DE DATOS ---
# Puedes reemplazar esto con: df = pd.read_csv('metricas_finales.csv')
df = pd.read_csv('metricas_finales.csv')

# Extraer número de nodos como entero para ordenar correctamente
df['Nodos'] = df['Escenario'].str.extract('(\d+)').astype(int)

# Configuración de Estilo Profesional
sns.set_theme(style="whitegrid", font="sans-serif")
plt.rcParams.update({'font.size': 12, 'font.family': 'sans-serif'})

# =============================================================================
# GRÁFICO A: Escalabilidad de Latencia (Ley de Amdahl)
# =============================================================================
plt.figure(figsize=(10, 6))
ax = sns.lineplot(data=df, x='Nodos', y='Latencia_Fria_s', marker='o', markersize=9, linewidth=3, color='#E63946', label='Latencia Fría')

# Estilizado
plt.title('A. Escalabilidad de Latencia: Tiempo de Respuesta vs Nodos', fontsize=16, fontweight='bold', pad=20)
plt.xlabel('Número de Nodos (Workers)', fontsize=12, fontweight='bold')
plt.ylabel('Latencia Promedio (segundos)', fontsize=12, fontweight='bold')
plt.xticks(df['Nodos'])
plt.grid(True, which='major', linestyle='--', alpha=0.7)
plt.legend(frameon=True, fancybox=True, shadow=True, loc='upper right')

# Etiquetas de valor
for x, y in zip(df['Nodos'], df['Latencia_Fria_s']):
    plt.text(x, y + 0.01, f'{y:.4f}s', ha='center', va='bottom', fontsize=10, fontweight='bold', color='#E63946')

plt.tight_layout()
plt.savefig('grafico_A_latencia.png', dpi=300)
plt.show()

# =============================================================================
# GRÁFICO B: Costo de Recursos (Trade-off de Memoria) - Doble Eje
# =============================================================================
fig, ax1 = plt.subplots(figsize=(10, 6))

color_ram = '#1D3557' # Azul oscuro profesional
color_lat = '#E63946' # Rojo contraste

# Eje Y1: RAM (Barras)
ax1.set_xlabel('Número de Nodos', fontsize=12, fontweight='bold')
ax1.set_ylabel('RAM Total Clúster (MB)', color=color_ram, fontsize=12, fontweight='bold')
bars = ax1.bar(df['Nodos'], df['RAM_Pico_MB'], color=color_ram, alpha=0.6, label='RAM Total', width=0.5, edgecolor='black')
ax1.tick_params(axis='y', labelcolor=color_ram)
ax1.grid(False) # Quitamos grid vertical para limpieza

# Etiquetas RAM
for bar in bars:
    height = bar.get_height()
    ax1.text(bar.get_x() + bar.get_width()/2., height,
             f'{int(height)} MB', ha='center', va='bottom', fontsize=9, color=color_ram, fontweight='bold')

# Eje Y2: Latencia (Línea)
ax2 = ax1.twinx()
ax2.set_ylabel('Latencia (s)', color=color_lat, fontsize=12, fontweight='bold')
line = ax2.plot(df['Nodos'], df['Latencia_Fria_s'], color=color_lat, marker='o', markersize=8, linewidth=3, linestyle='--', label='Latencia')
ax2.tick_params(axis='y', labelcolor=color_lat)
ax2.grid(True, linestyle=':', alpha=0.5)

# Títulos y Leyenda combinada
plt.title('B. Trade-off: Costo de Memoria vs Mejora de Velocidad', fontsize=16, fontweight='bold', pad=50)
plt.xticks(df['Nodos'])

# Leyenda manual para unir barras y líneas
from matplotlib.lines import Line2D
from matplotlib.patches import Patch
legend_elements = [Patch(facecolor=color_ram, alpha=0.6, edgecolor='black', label='Costo RAM'),
                   Line2D([0], [0], color=color_lat, lw=3, linestyle='--', marker='o', label='Beneficio Latencia')]
ax1.legend(handles=legend_elements, loc='upper center', bbox_to_anchor=(0.5, 1.12), ncol=2, frameon=False, fontsize=11)

plt.tight_layout()
plt.savefig('grafico_B_tradeoff.png', dpi=300)
plt.show()

# =============================================================================
# GRÁFICO C: Impacto de la Caché (Comparativa Logarítmica)
# =============================================================================
plt.figure(figsize=(9, 6))

# Datos promedio para comparación general
avg_cold = df['Latencia_Fria_s'].mean()
avg_cache = df['Latencia_Cache_s'].mean()
impact_data = pd.DataFrame({
    'Tipo': ['Petición Procesada\n(Cluster Distribuido)', 'Petición Caché\n(Redis In-Memory)'],
    'Tiempo': [avg_cold, avg_cache],
    'Color': ['#457B9D', '#2A9D8F']
})

ax = sns.barplot(x='Tipo', y='Tiempo', data=impact_data, palette=impact_data['Color'].tolist(), edgecolor='black')

# Estilo
plt.title('C. Impacto de Redis: Comparativa de Latencia', fontsize=16, fontweight='bold', pad=20)
plt.ylabel('Tiempo de Respuesta (segundos) - Escala Log', fontsize=12, fontweight='bold')
plt.xlabel('')
plt.yscale('log') # ESCALA LOGARÍTMICA CRÍTICA

# Etiquetas de valor
for i, v in enumerate(impact_data['Tiempo']):
    ax.text(i, v, f'{v:.4f}s', ha='center', va='bottom', fontsize=14, fontweight='bold', color='black')
    
    # Calcular mejora porcentual para anotación
    if i == 1:
        improvement = (avg_cold / avg_cache)
        plt.text(i, v/10, f'¡{improvement:.0f}x más rápido!', ha='center', va='top', fontsize=11, color='#2A9D8F', fontweight='bold')

plt.grid(axis='y', linestyle='--', alpha=0.5)
plt.tight_layout()
plt.savefig('grafico_C_cache.png', dpi=300)
plt.show()